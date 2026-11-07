# Segment 6: Office controls in Apple Home — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hugh can turn Office on and off and set its heating temperature from Apple Home and Siri, without values bouncing, through the same control path as the web UI.

**Architecture:** The HomeKit bridge's `OnSetRemoteValue` handlers turn Home writes into `control.Intent`s (`source:"homekit"`). `Bridge.Sync` reads `Controller.Overlay(snapshot)`, so pending values hold until the cloud confirms them. Units not in `--writable-units` keep segment 4's read-only behaviour.

**Tech Stack:** Go, brutella/hap.

**Spec:** `docs/superpowers/specs/2026-11-07-comfortd-design.md` ("HomeKit", "Control"). Builds on segment 4 (`homekit.Bridge`, `onSet*` methods, `ErrReadOnly`) and segment 5 (`control.Controller`, `Intent`, `LastMode`, `Overlay`, `Writable`).

## Global Constraints

- All earlier constraints hold.
- **Gate before any code (Task 0):** look at the schedule evidence in history first.
- Mapping from Home writes (spec):
  - Active 0 → `Mode "off"`.
  - Active 1 while the unit is off → `Mode LastMode(slug)`, *unless an intent already queued in this coalescing window sets a mode*.
  - Active 1 while the unit is on → nothing.
  - Target Heat → `Mode "heat"`.
  - HeatingThreshold v → `SpHeat v`.
- `TargetHeaterCoolerState.ValidVals` stays `[1]` (Heat).
- An `ErrNotWritable` or `ErrInvalid` from control is returned from `OnSetRemoteValue`, so Home shows the failure.

## Review Focus

1. **Home sends Active=1 and Target=Heat in either order while the unit is off and the last mode was `vent`:** the result must be heat either way. Pinned in Task 1 (`TestActiveAfterTargetKeepsHeat`, `TestTargetAfterActiveIsHeat`).
2. **Slider drag** (thresholds 19.5, 20, 20.5, 21 within half a second): one command, value 21, and Home's slider doesn't jump back. Pinned in Task 1 (`TestSliderBurstOneCommand`) and Task 1 (`TestSyncUsesOverlay`).
3. **Setting Active=1 while the unit is already on in vent mode:** sends nothing (it must not switch the unit to heat). Pinned in Task 1 (`TestActiveOnWhenAlreadyOnNoop`).
4. **House (not writable):** Home writes are still rejected. Pinned in Task 1 (`TestNotWritableStillRejected`).
5. **Siri "set office to 40 degrees":** rejected with an error; nothing sent. HAP clamps to max first, so assert on the clamped value being accepted, or on rejection if the bridge receives an out-of-range value. Pinned in Task 1 (`TestThresholdOutOfRange`).

---

### Task 0: Schedule evidence (no code)

- [ ] **Step 1:** `ssh bigthink curl -s 'http://127.0.0.1:9879/v1/history?unit=office&limit=1000'`. List every change to `device.operationMode`, `device.spHeat`, `zone.holdMode.*` and `zone.adapter.*` that has no `command` record in the 60 s before it.
- [ ] **Step 2:** Compare their times against each other: same time every day suggests the schedule; irregular suggests the app or the remote.
- [ ] **Step 3:** Check whether a segment 5 command was reverted within a few hours by such an unexplained change.
- [ ] **Step 4:** Write the finding in `docs/api-inventory.md` (open question 3) and `plan.md`.
- [ ] **Step 5:** Decide. If commands get reverted by the schedule, **stop**: tell Hugh what was seen, and write a schedule/hold segment plan instead. Read `holdMode` in the zone fixture for how the app's holds look. Otherwise continue.

### Task 1: Home writes through control

**Files:** Modify `internal/homekit/bridge.go`, `internal/homekit/bridge_test.go`, `cmd/comfortd/main.go` (pass the Controller to the bridge)

**Interfaces:**
- Consumes: `control.Controller` (segment 5).
- Produces:
  - `homekit.Options` gains `Control Controller` (nil = read-only everywhere).
  - The package-local interface:
    ```go
    type Controller interface {
        Apply(slug string, in control.Intent, source string) error
        Overlay(s store.Snapshot) store.Snapshot
        Writable(slug string) bool
        LastMode(slug string) string
        PendingMode(slug string) (string, bool) // add to control.Controller: a mode queued but not yet sent
    }
    ```
  - `onSetActive`, `onSetTarget`, `onSetHeatThresh` implement the mapping. `Sync` applies `FromDevice` to `Overlay(snapshot)`.
  - In `main.go`, the controller's `OnChange` also calls `bridge.Sync(slug)` (segment 5 wired it to the push hub only). The bridge's store subscription stays registered after `ctl.Observe`.
- [ ] **Step 1: Failing tests** (real `control.Controller` on kumotest, Window 50 ms):
  - `TestActiveZeroSendsOff` → command `{"operationMode":"off"}`.
  - `TestActiveOnFromOffUsesLastMode`: last mode `vent`, unit off, Active 1 → `{"operationMode":"vent"}`.
  - `TestActiveAfterTargetKeepsHeat`: unit off, last mode vent; Target Heat then Active 1 within the window → one command `{"operationMode":"heat"}`.
  - `TestTargetAfterActiveIsHeat`: Active 1 then Target Heat → `{"operationMode":"heat"}`.
  - `TestActiveOnWhenAlreadyOnNoop`: unit on in vent, Active 1 → no command.
  - `TestSliderBurstOneCommand`: thresholds 19.5, 20, 20.5, 21 → one command `{"spHeat":21}`.
  - `TestSyncUsesOverlay`: after the threshold intent, before the poll, `Sync` → threshold characteristic 21 (not the polled 19).
  - `TestNotWritableStillRejected`: house → error, no command.
  - `TestThresholdOutOfRange`: `onSetHeatThresh("office", 45)` → error wrapping `control.ErrInvalid`, no command.
- [ ] **Step 2–4:** FAIL → implement (`PendingMode` in `control` first, with its own test `TestPendingMode`) → PASS with `-race`.
- [ ] **Step 5:** Gate green; commit `homekit: Office controls through control`; push.

### Task 2: Deploy and try it (Hugh)

- [ ] **Step 1:** Bump the input and deploy (segment 1 Task 8 Steps 1–5). There are no config changes: `writableUnits` already lists office, and `homekit.units` already lists office.
- [ ] **Step 2:** Tell Hugh it's live and ask him to try in Home:
  - turn Office off;
  - turn it on;
  - drag the setpoint;
  - ask Siri to set Office to 20 degrees.
  For each, check history: a `command` record with `source:"homekit"`, a matching device change, and no `unconfirmed`.
- [ ] **Step 3:** Log in `plan.md` and the probe log. Mark the MVP done for Office. Next is segment 7 (House, then features), using the recipe in the spec.
