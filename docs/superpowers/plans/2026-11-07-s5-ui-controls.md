# Segment 5: Office controls in the web UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** From `https://comfortd.wg/`, Hugh can turn Office off, set heat or fan-only, and nudge the heat setpoint, through a single write path that HomeKit will reuse in segment 6. House remains unwritable.

**Architecture:** `internal/control.Controller` is the only caller of `kumo.Client.SendCommand`. It enforces the allowlist and validation, coalesces intents over 1 s into one multi-field command, holds pending values until a poll confirms them (or 30 s pass), kicks the poller, and records every command in history. UI views and (later) HomeKit read snapshots through `Controller.Overlay`, so pending values show instead of stale polled ones.

**Tech Stack:** Go, templ, datastar.

**Spec:** `docs/superpowers/specs/2026-11-07-comfortd-design.md` ("Control"). Builds on segments 1–4 (`kumo.Client.SendCommand`, `store`, `history`, `poller.Kick`, `ui.CardView`, `templates.UnitSection`).

## Global Constraints

- **The first live write is agreed with Hugh in chat before it is sent** (Task 4). Tests use `kumotest` only.
- Allowed modes in this segment, exactly: `off`, `heat`, `vent`. `spHeat` rounded to the nearest 0.5, then must lie within the profile's heat min/max. No profile → `ErrInvalid`.
- Coalescing window 1 s; pending hold timeout 30 s; `Kick` after every send.
- `--writable-units` default empty; bigthink sets `[ "office" ]`. Nothing is ever sent at startup or as a result of a poll.
- History records: one per sent field, `endpoint:"command"`, `path:<field>`, `old:"null"`, `new:<JSON value>`, `source:"ui"|"api"|"homekit"`. On hold timeout: `endpoint:"unconfirmed"`, `path:<field>`, `new:<sent value>`, `old:<polled value>`.
- `/ui/*` POSTs require the `Datastar-Request: true` header (sent by datastar's `@post`); `/v1/*` POSTs require `Content-Type: application/json`. Both reject with 403/415 otherwise. This stops plain cross-site form posts.
- `lastmode.json` (state dir): `{"<slug>":"<mode>"}`, updated whenever a poll shows a mode that is in the allowed set and isn't `off` (currently `heat` or `vent`). Other modes (e.g. `cool` set from the app) leave it unchanged, so `LastMode` never returns a mode `Apply` would reject. Default `heat`.
- **Change notification:** `Options.OnChange func(slug string)` is called (outside the controller's lock) whenever pending state or `LastError` changes for a unit: queued, sent, confirmed, timed out, failed. `main` wires it to `hub.Publish(pushEvent{Slug: slug})`, and in segment 6 to `bridge.Sync(slug)`.
- **Subscription order:** `store.Store` calls subscribers in registration order (pin with a test in `internal/store` if segment 1 didn't). `main` registers `ctl.Observe` **before** the push hub and the HomeKit bridge, so their re-render already sees a cleared hold.

## Review Focus

1. **Button mashing** (setpoint + five times in a second): exactly one command is sent, with the final value. Pinned in Task 1 (`TestCoalescesBurst`).
2. **A schedule or the app changes the unit while a hold is pending:** the hold times out and the real value shows, with an `unconfirmed` record. Pinned in Task 1 (`TestHoldTimesOut`).
3. **`spHeat` sent as 21 and polled back as 21.0 or `21`:** counts as confirmed. Pinned in Task 1 (`TestHoldConfirmsNumerically`).
4. **Send failure (cloud 500/timeout):** no pending hold, an error is visible in the UI, and nothing is retried automatically. Pinned in Task 1 (`TestSendFailureNoHold`).
5. **A write to House** through the API or a hand-made UI request is refused. Pinned in Task 1 (`TestNotWritable`) and Task 2 (`TestCommandEndpointForbidden`).

---

### Task 1: Controller

**Files:** Create `internal/control/control.go`, `internal/control/control_test.go`

**Interfaces:**
- Consumes: `store.Store`, `store.Update`, `history.Log`, `kumo.Profile`.
- Produces:
  ```go
  type Intent struct{ Mode *string; SpHeat *float64 }
  type Sender interface{ SendCommand(ctx context.Context, serial string, commands map[string]any) error }
  type Options struct {
      Sender      Sender
      Store       *store.Store
      History     *history.Log
      Kick        func(slug string)
      Writable    []string
      Window      time.Duration // 1s
      HoldTimeout time.Duration // 30s
      LastModePath string
      OnChange    func(slug string)
      Now         func() time.Time
  }
  var ErrNotWritable = errors.New("control: unit not writable")
  var ErrInvalid = errors.New("control: invalid intent") // wrap with the reason: fmt.Errorf("%w: spHeat 40 outside 10–31", ErrInvalid)
  func New(o Options) *Controller
  func (c *Controller) Apply(slug string, in Intent, source string) error // validates and queues; returns immediately
  func (c *Controller) Overlay(s store.Snapshot) store.Snapshot          // Device fields replaced by pending values
  func (c *Controller) Pending(slug string) map[string]any
  func (c *Controller) LastError(slug string) string                      // last send error, cleared on next success
  func (c *Controller) LastMode(slug string) string                       // default "heat"
  func (c *Controller) Observe(u store.Update)                            // subscribe to the store: confirm holds, update last mode
  func (c *Controller) Run(ctx context.Context)                           // flush and timeout timers
  func (c *Controller) Writable(slug string) bool
  ```
- [ ] **Step 1: Failing tests** (kumotest as the Sender via a real `kumo.Client`; short `Window` of 50 ms and `HoldTimeout` of 300 ms):
  - `TestNotWritable`: `Apply("house", …)` → `ErrNotWritable`; nothing sent.
  - `TestInvalidMode`: mode `cool` → `ErrInvalid`; `spHeat` 40 → `ErrInvalid`; `spHeat` 20.3 → sent as 20.5.
  - `TestCoalescesBurst`: `Apply` spHeat 20, 20.5, 21 and mode heat within 30 ms → after 100 ms exactly one `srv.Commands()` entry `{"operationMode":"heat","spHeat":21}`.
  - `TestOverlayShowsPending`: after the send, before a poll, `Overlay(snapshot).Device.SpHeat` == 21.
  - `TestHoldConfirmsNumerically`: poll returns `spHeat` 21 (the fake stores 21 as float64) → `Pending` empty; no `unconfirmed` record.
  - `TestHoldTimesOut`: `srv.SetField` overrides `spHeat` back to 19 straight after the send → after 400 ms `Pending` is empty and history has an `unconfirmed` record with `new:"21"`, `old:"19"`.
  - `TestSendFailureNoHold`: `FailNext("/devices/send-command", 500, 1)` → `Pending` empty, `LastError` non-empty, `srv.Commands()` empty. The next `Apply` succeeds and clears `LastError`.
  - `TestCommandRecorded`: a sent command → history records `endpoint:"command"`, `source:"ui"`.
  - `TestKickCalled`: the `Kick` stub is called with `"office"` after the send.
  - `TestLastModePersists`: an `Observe`d device update with mode `vent` → `LastMode("office")` == `vent` and `lastmode.json` contains it. A later `off` or `cool` doesn't change it, and a new Controller on the same path returns `vent`.
  - `TestOnChangeCalled`: `OnChange` stub sees `"office"` on queue, on send, and again when the hold confirms; and again on timeout in `TestHoldTimesOut`.
  - `TestNoSendWithoutApply`: run the Controller with polls for 200 ms and no `Apply` → `srv.Commands()` empty.
- [ ] **Step 2–4:** FAIL → implement → PASS with `-race`. **Step 5:** commit `control: single write path with coalescing and pending hold`.

### Task 2: Command API and UI controls

**Files:** Modify `cmd/comfortd/server.go`, `cmd/comfortd/main.go` (flag `-writable-units`; build Controller; `store.Subscribe(ctl.Observe)`; views use `ctl.Overlay`), `cmd/comfortd/ui/card.go`, `cmd/comfortd/ui/templates/card.templ`; Create `cmd/comfortd/control_http.go`, `cmd/comfortd/control_http_test.go`; Modify `nix/module.nix` (pass `--writable-units`; remove the remaining assertion)

**Interfaces:**
- Produces:
  - `POST /v1/units/{slug}/command`, JSON body `{"mode":"heat","spHeat":21}` (either field optional) → 202 `{"queued":true}`; 400 `{"error":…}` for `ErrInvalid`; 403 for `ErrNotWritable`; 404 for an unknown slug; 415 without JSON content type.
  - `POST /ui/units/{slug}/mode?m=<mode>` and `POST /ui/units/{slug}/spheat?delta=<±0.5>` → 204 (the result arrives over SSE); same error mapping; 403 without `Datastar-Request`.
  - `ui.CardControl` (defined in segment 3, zero until now) is filled by `main` from the controller: `Writable(slug)`, `Pending(slug)` formatted as display strings, `LastError(slug)`. It's passed to `BuildUnitView`. The view's snapshot is `ctl.Overlay(snapshot)`.
  - The card renders, only when `Writable`:
    - buttons Off / Heat / Fan only, each `data-on:click="@post('/ui/units/{slug}/mode?m=…')"`;
    - setpoint − / +, each `@post('/ui/units/{slug}/spheat?delta=∓0.5')`;
    - a "pending" badge on pending fields and the last error text.
  - The badge appears straight away through `OnChange` (no extra publish needed in the handler).
- [ ] **Step 1: Failing tests:**
  - `TestCommandEndpointQueues` (writable office) → 202, then within 2 s `srv.Commands()` has the command.
  - `TestCommandEndpointForbidden` (house) → 403.
  - `TestCommandEndpointInvalid` (`spHeat` 99) → 400.
  - `TestCommandEndpointNeedsJSON` → 415.
  - `TestUIPostNeedsDatastarHeader` → 403 without it, 204 with it.
  - `TestCardButtonsOnlyWhenWritable`: office card has `/ui/units/office/mode?m=heat`; house card has no `@post`.
- [ ] **Step 2–4:** FAIL → implement → PASS. `templ generate`. Manual check with `-fake -writable-units office`.
- [ ] **Step 5:** Gate green; commit `ui: Office controls through control`; push.

### Task 3: Deploy without writing

- [ ] Add `writableUnits = [ "office" ];` to `services/comfortd.nix`, bump the input, deploy (segment 1 Task 8 Steps 1–5).
- [ ] Verify: Office's card has buttons and House's has none. `curl -s -X POST -H 'Content-Type: application/json' -d '{"spHeat":21}' http://127.0.0.1:9879/v1/units/house/command` → 403. **Don't press anything yet.**

### Task 4: First live write (Hugh agrees first)

- [ ] **Step 1:** Tell Hugh Office's current mode and setpoint (from the card) and propose one change, e.g. "setpoint +0.5 (19 → 19.5), then back". Wait for his yes.
- [ ] **Step 2:** Press the button, or use the `/v1` command with `source` `api`. Watch for:
  - the card showing pending, then confirmed within about 5–15 s;
  - history showing `command` and then the `device` change;
  - no `unconfirmed` record.
  Ask Hugh to confirm the Comfort app shows the change.
- [ ] **Step 3:** Revert if Hugh wants. Add a row to the probe log in `docs/api-inventory.md` and a Log line in `plan.md`. Commit, push.
