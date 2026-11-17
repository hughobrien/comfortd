# Hold Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A radio group on the Schedule section's season line that sets and ends a Comfort hold on a writable unit: No hold / Until next event / Until [time] / Permanent.

**Architecture:** `pkg/kumo` gets `SetHold`/`EndHold` (the v4 site hold endpoint) and parses `holdType`. `internal/control` wraps them behind the `Writable` guard with history and a kick poll. `cmd/comfortd` adds a view (`ui.BuildHold`), a templ control outside the schedule island, and `POST /ui/units/{slug}/hold`. All tests run against `kumotest`.

**Tech Stack:** Go, templ (generated `*_templ.go` are committed), datastar (SSE patches), the in-repo `kumotest` fake.

**Spec:** `docs/superpowers/specs/2026-11-17-hold-control-design.md`. Request shapes and evidence: `docs/api-inventory.md` row `POST /v4/sites/{siteId}/hold`.

## Global Constraints

- `POST /v4/sites/{siteId}/hold`, body `{siteId, endTime?, zones:[{id, enabled, type:"hold"}]}`; success is **201**.
- `endTime` is epoch **seconds** as a JSON number; a string gives 400 `{"error":{"endTime":"Expected number, received string"}}`.
- Permanent = `endTime: null` (key present). The cloud stores it ~100 years ahead; never show that date.
- End = `enabled:false`, no `endTime`. Ending re-applies the schedule's current event.
- No command reaches a real AC unless Hugh agrees to that specific change (CLAUDE.md). Tests use `kumotest` only. Live check: Office only, with Hugh's agreement.
- SPDX header on new Go files (`// SPDX-License-Identifier: GPL-3.0-or-later`). No serials, MACs or account details in commits (public repo).
- Style: stdlib first, small files, table tests, `slog`. Commit per task. Gate: `just gate` (check printed `NIX EXIT=`).
- Templates: `go run github.com/a-h/templ/cmd/templ@v<pinned> generate` (pin = the version in `go.mod`), commit the `*_templ.go`.

## Review Focus

1. A time earlier than now or past a sane horizon (reject; the UI picks tomorrow for a past clock time, the control layer rejects `until` not after now).
2. Unwritable unit (House): endpoint must answer 403 and send nothing, even though the template hides the control.
3. Request without the `Datastar-Request: true` header: 403, nothing sent.
4. App-made hold of each type (`until_next_event`, `until_specific_time`, `permanent`): the radios show the right one; permanent never shows its far-future date.
5. A failed cloud write (400/500): an error message in the control, no history record, no kick.

---

### Task 1: kumo client — parse `holdType`, add `SetHold` / `EndHold`

**Files:**
- Modify: `pkg/kumo/types.go` (Zone, ParseZones)
- Create: `pkg/kumo/hold.go`
- Test: `pkg/kumo/hold_test.go`, `pkg/kumo/client_test.go` (zone parse case, if the existing zone test lives there)

**Interfaces:**
- Produces: `Zone.HoldType string` (cloud value, e.g. `until_next_event`, `until_specific_time`, `permanent`, `unset`, or empty); `func (c *Client) SetHold(ctx context.Context, siteID, zoneID string, until *time.Time) error` (nil `until` = permanent); `func (c *Client) EndHold(ctx context.Context, siteID, zoneID string) error`. `url()` already routes `/v4/` paths.

- [ ] **Step 1: Failing tests.** `TestParseZonesHoldType`: a zone JSON with `holdMode:{enabled:true,holdType:"permanent",endTime:"2126-11-17T02:56:41.000Z"}` parses to `HoldType == "permanent"`, `HoldEnabled`. `TestSetHoldBody`/`TestEndHoldBody`: use a plain `httptest` server that records method, path and decoded JSON body, and assert: path `/v4/sites/S/hold`; SetHold with a time sends `endTime == float64(unix seconds)` as a JSON number; SetHold(nil) sends the key `endTime` with value `nil`; EndHold sends no `endTime` key and `zones[0].enabled == false`; all send `zones[0] == {id:Z, type:"hold"}` plus `enabled`, and `siteId == "S"`. A 400 reply returns `*HTTPError`.
- [ ] **Step 2: Run** `go test ./pkg/kumo/ -run 'Hold|ParseZones' -v`; expect FAIL (undefined).
- [ ] **Step 3: Implement** the signatures above in `hold.go` using `c.do(ctx, "POST", "/v4/sites/"+siteID+"/hold", body)`; extend `ParseZones` with `HoldType string \`json:"holdType"\``.
- [ ] **Step 4: Run** the same command; expect PASS.
- [ ] **Step 5: Commit** `feat(kumo): hold create/end requests and holdType`.

### Task 2: kumotest — the hold endpoint and zone hold state

**Files:**
- Modify: `pkg/kumo/kumotest/server.go` (route, handler, state, `handleZones`)
- Test: `pkg/kumo/kumotest/server_test.go`

**Interfaces:**
- Consumes: Task 1's request shapes.
- Produces: route `POST /v4/sites/{site}/hold`; `type HoldWrite struct { Zone string; Enabled bool; EndTime *int64; Permanent bool }`; `func (s *Server) HoldWrites() []HoldWrite`. After a write, `GET /sites/{site}/zones` returns that zone's `holdMode` as `{enabled, type:"hold", holdType, origin:"adapter", endTime(RFC3339 ms Z)}`: enabled+number → `until_specific_time`; enabled+null → `permanent` with endTime now+100y; disabled → `permanent`, endTime now.

- [ ] **Step 1: Failing test** `TestHoldEndpoint`: POST with a string `endTime` → 400 and body contains `Expected number, received string`; POST with a number → 201, body has `holdModes[0].holdType == "until_specific_time"`; zones GET then shows `holdMode.enabled == true` for that zone; end → `enabled == false`; `HoldWrites()` has the three accepted/rejected writes as expected (rejected ones are not recorded).
- [ ] **Step 2: Run** `go test ./pkg/kumo/kumotest/ -run Hold -v`; expect FAIL.
- [ ] **Step 3: Implement** handler `handleHold`, per-zone hold state under `s.mu`, applied in `handleZones` (fixture zones without a write are unchanged). Site must equal `SiteID`, else 404.
- [ ] **Step 4: Run** the full `go test ./pkg/kumo/...`; expect PASS.
- [ ] **Step 5: Commit** `test(kumotest): hold endpoint`.

### Task 3: control layer — `Holds`, `SetHold`, `EndHold`

**Files:**
- Create: `internal/control/hold.go`
- Modify: `internal/control/control.go` (Options only), `cmd/comfortd/main.go` (pass `Holds: client`)
- Test: `internal/control/hold_test.go`

**Interfaces:**
- Consumes: `Writer` methods from Task 1 (`*kumo.Client` satisfies the new interface).
- Produces:
  ```go
  type HoldWriter interface {
      SetHold(ctx context.Context, siteID, zoneID string, until *time.Time) error
      EndHold(ctx context.Context, siteID, zoneID string) error
  }
  // Options gains: Holds HoldWriter // nil = hold changes are off
  func (c *Controller) SetHold(ctx context.Context, slug string, until *time.Time, source string) error // nil until = permanent
  func (c *Controller) EndHold(ctx context.Context, slug, source string) error
  ```
  Errors: `ErrNotWritable` when the unit is not in `Writable` or `Holds` is nil; `ErrInvalid`-wrapped when the unit is unknown, `SiteID`/`ZoneID` are empty, or `until` is not after `c.o.Now()`. On success: one history record (`endpoint "hold"`, `path "holdMode"`, old/new = JSON of `{enabled, holdType, endTime}` from the snapshot's zone before, and the requested state after, `source` as given) then `c.o.Kick(slug)` if set. On cloud error: no record, no kick, error returned.

- [ ] **Step 1: Failing tests** with a fake `HoldWriter` recording calls: writable unit → call made with the snapshot's SiteID/ZoneID, one history record with `Endpoint=="hold"`, Kick called; `TestHoldNotWritable` (House slug) → `ErrNotWritable`, no call; `TestHoldPastTime` → `ErrInvalid`, no call; `TestHoldWriteFails` → error, no history, no kick; nil `until` → fake receives nil.
- [ ] **Step 2: Run** `go test ./internal/control/ -run Hold -v`; expect FAIL.
- [ ] **Step 3: Implement** in `hold.go` using the existing `record`, `Writable`, `sendTimeout`, `jsonOf`; follow `ApplySchedule`'s guard order.
- [ ] **Step 4: Run** `go test ./internal/control/ ./cmd/comfortd/...`; expect PASS.
- [ ] **Step 5: Commit** `feat(control): set and end holds`.

### Task 4: hold view (`ui.BuildHold`) and annotations

**Files:**
- Modify: `cmd/comfortd/ui/schedule.go` (`ScheduleView.NextAt time.Time`, set in `BuildSchedule`), `cmd/comfortd/ui/card.go` (`CardView.Hold HoldView`, filled in `BuildCard`), `internal/annotate/annotations.toml`
- Create: `cmd/comfortd/ui/hold.go`
- Test: `cmd/comfortd/ui/hold_test.go`

**Interfaces:**
- Consumes: `kumo.Zone.HoldType/HoldEnabled/HoldEnd` (Task 1).
- Produces:
  ```go
  type HoldView struct {
      Show    bool      // unit writable and a zone exists
      Mode    string    // "none" | "next" | "until" | "permanent"
      Time    string    // "15:04" in the unit's zone: the hold's end for "until", else the suggested time
      CanNext bool      // the schedule has a next event
      NextAt  time.Time
  }
  func BuildHold(s store.Snapshot, writable bool, now time.Time) HoldView
  func HoldTimeFromClock(s store.Snapshot, now time.Time, hhmm string) (time.Time, error) // today in the unit's zone, else tomorrow if not after now; error on a bad "HH:MM"
  ```
  Mode rules: not enabled → `none`; `permanent` → `permanent`; `until_next_event` → `next`; `until_specific_time` → `next` if `HoldEnd` equals the schedule's `NextAt`, else `until`; any other enabled value → `until` with `HoldEnd`.
- [ ] **Step 1: Failing table test** `TestBuildHold` over the Mode rules above (including permanent with a 2126 `HoldEnd` → `Time` empty/suggested, not 2126), writable false → `Show false`, no schedule → `CanNext false`; `TestHoldTimeFromClock`: 22:30 before it → today, after it → tomorrow, "25:99" → error.
- [ ] **Step 2: Run** `go test ./cmd/comfortd/ui/ -run Hold -v`; expect FAIL.
- [ ] **Step 3: Implement.** Reuse `scheduleLoc`/`BuildSchedule`. Add to `annotations.toml` a `holdMode.holdType` values table: `until_next_event` "Until the next event", `until_specific_time` "Until a set time", `permanent` "Permanent" (confidence `app-confirmed`; keep the existing note).
- [ ] **Step 4: Run** `go test ./cmd/comfortd/... ./internal/annotate/...`; expect PASS.
- [ ] **Step 5: Commit** `feat(ui): hold view`.

### Task 5: the radio control and `POST /ui/units/{slug}/hold`

**Files:**
- Modify: `cmd/comfortd/ui/templates/card.templ` (+ regenerated `card_templ.go`), `cmd/comfortd/ui/style.css`, `cmd/comfortd/control_http.go` or the router that calls `registerSchedule` (register the new route)
- Create: `cmd/comfortd/hold_http.go`
- Test: `cmd/comfortd/hold_http_test.go`; `cmd/comfortd/ui/templates/render_test.go` (render case)

**Interfaces:**
- Consumes: `ui.BuildHold`, `ui.HoldTimeFromClock` (Task 4); `Controller.SetHold/EndHold` (Task 3); `kumotest.HoldWrites` (Task 2); existing `scheduleGuard`-style checks (`uiOK`, `d.known`, `d.Ctl.Writable`, `d.ctlError`) and `ctlDeps` in tests.
- Produces: templ `HoldControl(c ui.CardView)` rendering `<div id="hold-ctl-{slug}" class="hold-ctl">` with four radios named `mode` (values `none`, `next`, `until`, `permanent`; `next` omitted when `!CanNext`), a `time` input (only meaningful with `until`) wrapped in a `data-ignore-morph` span, a one-line note "No hold ends the hold and applies the schedule's current settings", and `<p class="hold-msg" id="hold-msg-{slug}">`. It is rendered inside `scheduleSection` for writable units **outside** the `data-ignore-morph` schedule body, so unit patches keep it fresh; CSS places it on the same row as the season heading. Route: `POST /ui/units/{slug}/hold`, form fields `mode` and `at` (`HH:MM`).
- Behaviour: guard order `uiOK` (403 without `Datastar-Request: true`) → `d.known` → `Writable` (403) → parse. `none` → `EndHold`; `next` → `SetHold(NextAt)` (400-style message if no next event); `until` → `SetHold(HoldTimeFromClock(...))`; `permanent` → `SetHold(nil)`; source `"ui"`. Success: patch `#hold-msg-{slug}` with an empty message (the control itself refreshes from the kick poll's push). Failure: patch `#hold-msg-{slug}` with the error text (invalid-prefix stripped, HTML-escaped), class `err`.

- [ ] **Step 1: Failing tests** in `hold_http_test.go` (using `ctlDeps`, `post`, `dsHdr`, `formType` and `newMux(d)`): `TestHoldUntilTime` (`mode=until&at=HH:MM` → 200, one `HoldWrites` entry with the epoch for that clock time and `Enabled`); `TestHoldNextEvent` (EndTime equals the schedule's next event); `TestHoldPermanent` (`Permanent` true); `TestHoldEnd` (`Enabled` false); `TestHoldNeedsDatastarHeader` (403, no writes); `TestHoldUnwritableUnit` (`/ui/units/house/hold` → 403, no writes); `TestHoldBadTime` (`at=99:99` → 200 with an error message in the body, no writes); `TestHoldCloudError` (`srv.FailNext("/v4/sites/"+kumotest.SiteID+"/hold", 500, 1)` → message in body, no history record). In `render_test.go`: the card for a writable unit contains four `name="mode"` radios with the right one `checked`, and the card for a read-only unit has no `hold-ctl`.
- [ ] **Step 2: Run** `go test ./cmd/comfortd/... -run 'Hold' -v`; expect FAIL.
- [ ] **Step 3: Implement** `hold_http.go` (`registerHold(mux)` called beside `registerSchedule`), the templ and CSS (a flex row; radios small; `.hold-msg.err` reuses the error colour used by `.sched-msg.err`). Regenerate templ. Datastar wiring: each radio and the time input post the form with `data-on:change` using the same `@post(..., {contentType: 'form'})` call style as the schedule editor's Save; the time input posts only when `until` is selected.
- [ ] **Step 4: Run** `go test ./... && go test -race ./cmd/comfortd/... ./internal/control/...`; expect PASS.
- [ ] **Step 5: Look at it.** `go run ./cmd/comfortd -fake -state-dir /tmp/comfortd-dev` and open `http://127.0.0.1:9879/`; confirm the control sits on the season line, a click sends a request and the message line stays empty; delete `/tmp/comfortd-dev`.
- [ ] **Step 6: Commit** `feat(ui): hold control on the season line`.

### Task 6: docs, gate, deploy, live check

**Files:**
- Modify: `plan.md` (Log), `docs/api-inventory.md` (note the UI uses the endpoint), close/comment issues 12 and 13 (`tea comment` / `tea issues close`, login `forgejo`).

- [ ] **Step 1:** Update the docs above; `go test ./...`.
- [ ] **Step 2:** `just gate`; expect `NIX EXIT=0`.
- [ ] **Step 3:** Push to `main`; deploy to bigthink exactly per `plan.md` "How we work" (comfortd flake input only; stop on any unrelated diff).
- [ ] **Step 4: Live check, only after Hugh agrees to that step.** On Office: Until next event → card shows it; Permanent; No hold. Record results in `plan.md`; leave Office with no hold unless Hugh says otherwise.
- [ ] **Step 5: Commit and push** the Log entry.
