# Segment 3: annotations, unknown fields, status cards — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The explorer labels the fields we understand (with a confidence badge and decoded values), lists the ones we don't, and shows a read-only status card per unit above the raw tables.

**Architecture:** A hand-edited TOML file embedded in `internal/annotate` replaces the decode table in `docs/api-inventory.md` as the source of truth. `ui.BuildUnitView` joins rows with annotations; a new `ui.CardView` is built from the parsed snapshot.

**Tech Stack:** Go, `github.com/BurntSushi/toml` (newest), templ, datastar.

**Spec:** `docs/superpowers/specs/2026-11-07-comfortd-design.md` ("Annotations", "Cards"). Builds on segment 2 (`ui.Row`, `ui.UnitView`, `BuildUnitView`, `templates.UnitSection`).

## Global Constraints

- All segment 1 and 2 constraints hold.
- Confidence values, exactly: `app-confirmed`, `api-only`, `assumed`, `hypothesis`. Unknown confidence in the file is a load error.
- Annotation paths use the jsondiff path syntax and are matched per endpoint (`device`, `zone`, `profile`, `status`, `sensor`).
- Cards show values in °C with one decimal only when needed (`20.5`, `19`).
- The card is read-only in this segment: no buttons.
- New dependency `BurntSushi/toml`: refresh `vendorHash` (segment 1 Global Constraints).

## Review Focus

1. **Annotation file typo** (bad confidence, duplicate path): fails at startup with a clear message, not silently. Pinned in Task 1 (`TestLoadRejectsBadConfidence`, `TestLoadRejectsDuplicate`).
2. **A field missing from the device** (`spHeat` null, `roomTemp` absent): the card shows `–`, it doesn't panic. Pinned in Task 2 (`TestCardMissingValues`).
3. **A stale unit:** the card says so prominently, with the last-OK time. Pinned in Task 2 (`TestCardStale`).
4. **Raw value not in the annotation's `values` map** (e.g. a new `operationMode`): the row shows the raw value with "(undecoded)". Pinned in Task 1 (`TestDecodeUnknownValue`).
5. **Unknown-fields list ignores noisy and annotated paths** but includes array paths. Pinned in Task 2 (`TestUnknownFields`).

---

### Task 1: Annotation file and loader

**Files:** Create `internal/annotate/annotate.go`, `internal/annotate/annotations.toml`, `internal/annotate/annotate_test.go`; Modify `docs/api-inventory.md` (replace the "Decode table" section body with one line: "Moved to `internal/annotate/annotations.toml`.")

**Interfaces:**
- Produces:
  ```go
  type Field struct {
      Endpoint, Path, Label, Confidence, Note string
      Values map[string]string // raw compact JSON value → meaning, e.g. "\"vent\"" → "Fan only"
  }
  type Set struct{ … }
  func Load() (*Set, error)                       // parses the embedded file
  func Parse(data []byte) (*Set, error)
  func (s *Set) Lookup(endpoint, path string) (Field, bool)
  func (f Field) Decode(rawValue string) (string, bool) // meaning, true if in Values
  ```
  TOML shape:
  ```toml
  [[field]]
  endpoint = "device"
  path = "operationMode"
  label = "Mode"
  confidence = "app-confirmed"
  note = "heat not yet cross-checked against the app label"
  values = { '"vent"' = "Fan only", '"off"' = "Off (also sets power 0)", '"heat"' = "Heat" }
  ```
- [ ] **Step 1:** Seed `annotations.toml`.
  - From the decode table in `docs/api-inventory.md`, take every row, keeping its confidence. Where a field has several rows, they become one entry whose `values` map holds each value. The entry's confidence is the lowest of its rows, in the order hypothesis < assumed < api-only < app-confirmed. Put per-value confidence in `note`.
  - From the "Office — device state fields" table, take only fields not already covered, mapping tags as `[observed]` → `api-only` and `[comfort_HA]` → `assumed`. **Skip rows tagged `[untested]` and the combined diagnostic row** (`statusDisplay`, `twoFiguresCode`, …), so those stay in "unknown fields".
  - Also add `zone` entries `hasActiveSchedule`, `holdMode.enabled`, `holdMode.holdType` (`assumed`), and `profile` entries `[0].minimumSetPoints.heat` / `[0].maximumSetPoints.heat` (`api-only`).
- [ ] **Step 2: Failing tests:**
  - `TestLoadEmbedded`: `Load()` → no error; `Lookup("device","operationMode")` found, label "Mode".
  - `TestLoadRejectsBadConfidence`: a field with `confidence = "sure"` → error mentioning the path.
  - `TestLoadRejectsDuplicate`: the same endpoint+path twice → error.
  - `TestDecodeUnknownValue`: operationMode field, `Decode("\"cool\"")` → `("", false)`; `Decode("\"vent\"")` → `("Fan only", true)`.
- [ ] **Step 3–4:** implement (embed the TOML) → PASS.
- [ ] **Step 5:** Commit `annotate: embedded field annotations`.

### Task 2: Annotated rows, unknown fields, cards

**Files:** Modify `cmd/comfortd/ui/view.go`, `cmd/comfortd/ui/view_test.go`, `cmd/comfortd/ui/templates/unit.templ`; Create `cmd/comfortd/ui/card.go`, `cmd/comfortd/ui/card_test.go`, `cmd/comfortd/ui/templates/card.templ`; Modify `cmd/comfortd/main.go` (load annotations at startup; exit 1 on error)

**Interfaces:**
- Consumes: `annotate.Set` (Task 1).
- Produces:
  - `ui.Row` gains `Label, Decoded, Confidence, Note string`.
  - `ui.UnitView` gains `Card CardView` and `Unknown []Row` (device and zone rows with no annotation and not noisy).
  - `BuildUnitView` gains parameters `ann *annotate.Set` and `ctl CardControl` (passed through to `BuildCard`).
  - ```go
    // CardControl carries control state into the card. In this segment, callers always pass the zero value; segment 5 fills it.
    type CardControl struct {
        Writable  bool
        Pending   map[string]string // field → display value
        LastError string
    }
    type CardView struct {
        Control CardControl
        Mode, Power, SpHeat, SpCool, RoomTemp, Humidity string // "–" when missing
        Connected, Stale, ScheduleActive, HoldEnabled bool
        LastOK string // "15:04:05", or "never"
    }
    func BuildCard(s store.Snapshot, ctl CardControl, now time.Time, interval time.Duration) CardView
    ```
    `Mode` uses the annotation's decoded meaning when available (`Fan only`), else the raw mode.
  - `templates.UnitSection` renders the card first, then "Unknown fields", then the raw endpoint tables, whose rows now show label, decoded value and a badge (`class="conf-<confidence>"`).
- [ ] **Step 1: Failing tests:**
  - `TestRowsAnnotated`: office snapshot + `Load()` → the operationMode row has `Label "Mode"`, `Decoded "Heat"`, `Confidence "api-only"` or whatever the seeded file says (assert it equals `Lookup(...).Confidence`).
  - `TestUnknownFields`: `Unknown` contains `twoFiguresCode` (unannotated), does not contain `updatedAt` (noisy) or `operationMode` (annotated), and contains any array-indexed path present in the zone raw that isn't annotated.
  - `TestCardFromFixture`: office → `Mode "Heat"`, `SpHeat "19"`, `RoomTemp "20.5"`, `Humidity "–"`, `ScheduleActive true`; house → `Humidity "52"`.
  - `TestCardMissingValues`: device JSON without `spHeat` and `roomTemp` → both `"–"`.
  - `TestCardStale`: `LastDeviceOK` 4 intervals ago → `Stale` true; rendered card contains the text `stale`.
- [ ] **Step 2–4:** FAIL → implement → `templ generate` → PASS.
- [ ] **Step 5:** Commit `ui: annotations, unknown fields, status cards`.

### Task 3: Deploy

- [ ] Gate green → bigthink deploy (segment 1 Task 8 Steps 1–5, input bump only) → verify on `https://comfortd.wg/`: both cards show values matching the Comfort app (ask Hugh to glance at the app only if a value looks wrong), the unknown-fields list is present. Update `plan.md` Log; commit, push.
- [ ] From now on, when a finding lands, update `annotations.toml` (not the inventory decode table) in the same commit as the finding's Log line.
