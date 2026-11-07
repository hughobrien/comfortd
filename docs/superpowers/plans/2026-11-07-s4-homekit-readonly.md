# Segment 4: HomeKit, Office read-only — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Office appears in Apple Home as an air conditioner showing its current temperature, heat state and heating setpoint, kept in sync with the cloud. Home writes are rejected. Pairing survives restarts and a reboot.

**Architecture:** `internal/homekit` holds a pure mapping (`FromDevice`), persisted accessory IDs, and a bridge (brutella/hap) with one `TypeAirConditioner` accessory + `HeaterCooler` service per unit in `--homekit-units`. A store subscriber calls `Bridge.Sync`. Stale units answer reads with HAP status -70402 ("No Response").

**Tech Stack:** Go, `github.com/brutella/hap` (newest upstream release), Nix.

**Spec:** `docs/superpowers/specs/2026-11-07-comfortd-design.md` ("HomeKit"). Builds on segments 1–3 (`store.Snapshot`, `store.Unit`, `kumo.Device`, `kumo.Profile`, `deps`, `newMux`).

## Global Constraints

- All earlier constraints hold. Still no AC writes anywhere.
- hap API facts (read from brutella/hap source, 2026-11-07; re-check names against the version you install and note differences in the plan.md Log):
  - `service.NewHeaterCooler()` has fields `Active`, `CurrentHeaterCoolerState`, `TargetHeaterCoolerState`, `CurrentTemperature`.
  - Optional characteristics are added with `hc.AddC(c.C)`: `characteristic.NewHeatingThresholdTemperature()`. Its default range is 0–25, so set min/max explicitly.
  - `accessory.New(info, accessory.TypeAirConditioner)`; `acc.AddS(hc.S)`; `acc.Id` (uint64) settable.
  - Int: `SetValue(int) error`, `ValidVals []int` (on embedded `*C`), `OnSetRemoteValue(func(int) error)`.
  - Float: `SetValue(float64)`, `SetMinValue`, `SetMaxValue`, `SetStepValue`, `OnSetRemoteValue(func(float64) error)`.
  - Constants: `TargetHeaterCoolerStateAuto/Heat/Cool` = 0/1/2; `CurrentHeaterCoolerStateInactive/Idle/Heating/Cooling` = 0/1/2/3.
  - `C.ValueRequestFunc` lets a read return a non-zero HAP status.
- `TargetHeaterCoolerState.ValidVals = []int{1}` (Heat only).
- `HeatingThresholdTemperature`: min/max from the profile's `minimumSetPoints.heat`/`maximumSetPoints.heat` (Office 10/31), step 0.5. If the profile isn't known when the bridge starts, use 10/31.
- `CurrentTemperature`: min −20, max 60, step 0.5.
- Accessory IDs: `aids.json` in the state dir, `{"<serial>": <aid>}`, assigned from 2 upwards, never reused or renumbered.
- PIN: `pin.txt` in the state dir (8 digits). Generation and validation are adapted from `../keylight-hap/internal/bridge/pin.go` (same author, same license). Its functions are unexported and its `FormatPin` gives `1234-5678`, so copy the file, export what's listed in Task 2, and change `FormatPin` to Home's own `123-45-678` layout. Port its tests, with the format assertions changed to match.
- New dependency `brutella/hap`: refresh `vendorHash` (segment 1 Global Constraints).
- Flags: `-hap-port` (default 0), `-homekit-units` (comma list of slugs, default empty = HomeKit off), `-bridge-name` (default `comfortd`), `-force-stale` (comma list of slugs; for verifying "No Response" only; documented as a debug flag).
- bigthink: HAP port `net.ports.tcp.comfortdHomeKit` (9880), opened on the LAN in `network.nix` next to `keylightHomeKit`.

## Review Focus

1. **Cloud down at boot:** the bridge still publishes Office (from cached discovery) and shows "No Response" until the first good poll. Pinned in Task 3 (`TestBridgeBuildsWithoutSnapshot`).
2. **Unit switched to cool/auto from the Mitsubishi app:** Target state must not be set to a value outside `ValidVals` (that would error or get clamped); Current state still reflects cooling. Pinned in Task 1 (`TestMapCoolWhenOnlyHeatAllowed`).
3. **Adding House later must not renumber Office.** Pinned in Task 2 (`TestAssignAIDsStable`).
4. **Setpoint outside the HomeKit range** (a profile max of 31 with the default 25): must show the real value. Pinned in Task 3 (`TestThresholdRangeFromProfile`).
5. **A write from Home in read-only mode** is rejected with an error, not silently accepted and then reverted. Pinned in Task 3 (`TestRemoteWritesRejected`).

---

### Task 1: State mapping

**Files:** Create `internal/homekit/mapping.go`, `internal/homekit/mapping_test.go`

**Interfaces:**
- Produces:
  ```go
  type HKState struct {
      Active      int      // 0/1
      Current     int      // CurrentHeaterCoolerState
      Target      *int     // nil = leave unchanged
      HeatThresh  *float64 // nil = leave unchanged
      CurrentTemp *float64
  }
  func FromDevice(d kumo.Device, allowedTargets []int) HKState
  ```
  Implements the spec's mapping table exactly. `HeatThresh` = `d.SpHeat`, `CurrentTemp` = `d.RoomTemp`.
- [ ] **Step 1: Failing table test** `TestFromDevice` with rows:

| power | mode | allowed | want Active | Current | Target |
|---|---|---|---|---|---|
| 1 | heat | [1] | 1 | 2 | 1 |
| 0 | heat | [1] | 0 | 0 | nil |
| 0 | off | [1] | 0 | 0 | nil |
| 1 | vent | [1] | 1 | 1 | nil |
| 1 | dry | [1] | 1 | 1 | nil |
| 1 | cool | [1,2] | 1 | 3 | 2 |
| 1 | auto | [0,1,2] | 1 | 1 | 0 |
| 1 | autoHeat | [0,1] | 1 | 2 | 0 |
| 1 | autoCool | [0,1,2] | 1 | 3 | 0 |

  Plus `TestMapCoolWhenOnlyHeatAllowed`: power 1, cool, allowed [1] → Current 3, Target nil. Plus: `SpHeat` 19 → `*HeatThresh` 19; `RoomTemp` nil → `CurrentTemp` nil.
- [ ] **Step 2–4:** FAIL → implement → PASS. **Step 5:** commit `homekit: device to HeaterCooler mapping`.

### Task 2: Accessory IDs and PIN

**Files:** Create `internal/homekit/aids.go`, `internal/homekit/aids_test.go`, `internal/homekit/pin.go`, `internal/homekit/pin_test.go` (port from keylight-hap)

**Interfaces:**
- Produces: `func AssignAIDs(path string, serials []string) (map[string]uint64, error)` (load, add missing serials as max+1 starting at 2, save atomically 0600); `func LoadOrGeneratePin(stateDir string) (string, error)`; `func FormatPin(pin string) string` (`"123-45-678"`).
- [ ] **Step 1: Failing tests:**
  - `TestAssignAIDsFresh`: `[S1]` → `{S1:2}`.
  - `TestAssignAIDsStable`: `[S1]`, then `[S2,S1]` → `{S1:2,S2:3}`; then `[S2]` → S2 still 3, and S1 kept in the file.
  - keylight-hap's pin tests, ported.
- [ ] **Step 2–4:** FAIL → implement → PASS. **Step 5:** commit `homekit: persisted accessory IDs and PIN`.

### Task 3: Bridge

**Files:** Create `internal/homekit/bridge.go`, `internal/homekit/bridge_test.go`

**Interfaces:**
- Consumes: Tasks 1–2; `store.Snapshot`, `store.Unit`.
- Produces:
  ```go
  type Options struct {
      StateDir, BridgeName, Version string
      Port      int
      Units     []store.Unit                     // only the units to expose
      Snapshot  func(slug string) (store.Snapshot, bool)
      Interval  time.Duration                    // poll interval, for staleness
      Now       func() time.Time
      ForceStale map[string]bool
  }
  type Bridge struct{ Pin string; … }
  func New(o Options) (*Bridge, error)                   // builds accessories, AIDs, PIN, hap server
  func (b *Bridge) Sync(slug string)                     // re-read snapshot, apply FromDevice, update staleness
  func (b *Bridge) Run(ctx context.Context) error        // ListenAndServe
  func (b *Bridge) Accessories() []AccessoryInfo         // for the /homekit page
  type AccessoryInfo struct{ Slug, Name, Serial string; AID uint64; Stale bool }
  var ErrReadOnly = errors.New("comfortd: read-only in this version")
  ```
  Accessory info: Name = unit name, Manufacturer `Mitsubishi`, Model = device `modelNumber` if known else `Kumo`, SerialNumber = serial, Firmware = `Version`.
- [ ] **Step 1: Failing tests** (no network needed: build the bridge and inspect characteristics; don't call `Run`):
  - `TestBridgeBuilds`: two units, snapshot for office from the fixture → office accessory AID 2, `CurrentTemperature` 20.5, `HeatingThresholdTemperature` 19, `Active` 1, `CurrentHeaterCoolerState` 2.
  - `TestThresholdRangeFromProfile`: office profile → threshold min 10, max 31, step 0.5; a snapshot with `spHeat` 28 → value 28 (not clamped to 25).
  - `TestBridgeBuildsWithoutSnapshot`: `Snapshot` returns false → `New` succeeds, `Accessories()[0].Stale` true.
  - `TestSyncUpdates`: change the snapshot's `spHeat` to 21, `Sync("office")` → threshold 21.
  - `TestRemoteWritesRejected`: the functions registered with `OnSetRemoteValue` return `ErrReadOnly`. Make each one a named method (`b.onSetActive(slug, v)` etc.) so the test can call it directly.
  - `TestStaleReadReturnsCommFailure`: stale office → the registered `ValueRequestFunc` returns status -70402; fresh → 0.
- [ ] **Step 2–4:** FAIL → implement → PASS (`-race`). **Step 5:** commit `homekit: read-only bridge`.

### Task 4: Wiring, /homekit page, module

**Files:** Modify `cmd/comfortd/main.go`, `cmd/comfortd/server.go`; Create `cmd/comfortd/ui/templates/homekit.templ`, `cmd/comfortd/homekit_test.go`; Modify `nix/module.nix`

**Interfaces:**
- Produces: `GET /homekit` page: bridge name, formatted PIN, table of `AccessoryInfo`. Only when HomeKit is on, else 404. The UI is reachable only on loopback/wg via nginx, so showing the PIN there is acceptable.
- Module: when `homekit.enable`, pass `--hap-port`, `--homekit-units` (`lib.concatStringsSep ","`); `openFirewall` opens `homekit.port` and UDP 5353. Remove the `homekit` half of segment 1's not-implemented assertion (keep the `writableUnits` half).
- [ ] **Step 1: Failing tests:** `TestHomekitPage`: daemon with `-fake -homekit-units office -hap-port 0` → `/homekit` contains a `NNN-NN-NNN` PIN and `office`; without `-homekit-units` → 404. `TestUnknownHomekitUnit`: `-homekit-units nope` → `run` exits non-zero with a message naming `nope`.
- [ ] **Step 2–4:** FAIL → implement (start `bridge.Run` in a goroutine after the first poll attempt; subscribe `store` → `bridge.Sync`) → PASS.
- [ ] **Step 5:** Gate on bigthink → green. Commit `homekit: wire bridge, /homekit page, module options`. Push; Forgejo gate green.

### Task 5: Deploy, pair, verify

- [ ] **Step 1:** bigthink config edits:
  - `services/comfortd.nix` gets `homekit = { enable = true; port = net.ports.tcp.comfortdHomeKit; units = [ "office" ]; };`
  - `network.nix` adds `comfortdHomeKit` to the LAN `allowedTCPPorts` list.
  - Bump the flake input, then deploy per segment 1 Task 8 Steps 1–5.
- [ ] **Step 2:** Check `ss -tlnp | grep 9880` shows comfortd, and `https://comfortd.wg/homekit` shows the PIN.
- [ ] **Step 3 (Hugh):** Ask Hugh to add the bridge in Home using that PIN. Confirm with him:
  - Office shows the room temperature and setpoint matching the Comfort app.
  - Trying to change the setpoint in Home fails or reverts.
- [ ] **Step 4:** Verify "No Response": restart with `extraArgs = [ "-force-stale" "office" ]`, ask Hugh what Home shows, then remove the flag and redeploy. If Home doesn't show "No Response", record what it does show in the Log and in the spec's HomeKit section.
- [ ] **Step 5 (Hugh's OK needed):** Ask Hugh for a time to reboot bigthink. After the reboot, check:
  - `comfortd` is active.
  - No new `kumo login` in the journal (tokens survived), unless the access and refresh tokens both expired.
  - Home still shows Office without re-pairing.
  - `/var/lib/comfortd` has `aids.json` and `pin.txt`.
  See `~/.claude/projects/-Users-hugh/memory/bigthink-sandbox-cold-boot.md` if the unit fails on boot.
- [ ] **Step 6:** Log in `plan.md` (pairing done, No Response result, reboot result); commit, push.
