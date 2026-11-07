# comfortd — design

Status: approved by the agent that owns the design (Hugh delegated it, 2026-11-07). Plans in `docs/superpowers/plans/` implement this document segment by segment.

## Why

Hugh has two Mitsubishi Comfort (Kumo Cloud) air conditioners and finds the Mitsubishi app frustrating. He wants Apple Home control of the basics: on/off, mode, setpoint. A Go daemon on bigthink (his NixOS home server) bridges the Kumo cloud API to HomeKit, the same way `keylight-hap` and `breezyd` bridge their devices. A small web UI comes first as a live API explorer, because the API is unofficial and only partly understood. It then grows into status cards and controls.

## Goals

- Office (ductless MSZ-FX12NL) controllable from Home: Active, heat, heating setpoint, current temperature.
- Then House (ducted PVA-AA36NL) the same way.
- A web UI at `https://comfortd.wg/` that shows every raw API field live, highlights changes, and labels fields we understand.
- A history of every field change, so we learn the API by watching, not only by probing.
- Built and deployed in small segments, each one running on bigthink before the next starts.

## Non-goals

- Schedules, holds, away mode, energy data. They may come later as features.
- Cool, auto and dry from Home until each is probed on the real unit.
- Fan speed and vane, until segment 7 picks them.
- Fahrenheit (HomeKit is Celsius; the API is Celsius in 0.5 steps).
- A CLI, Prometheus metrics, Playwright tests (breezyd has them; this project doesn't need them yet).
- Any LAN/local control path.

## Anti-goals (failure even if it "works")

- **A write to an AC that Hugh didn't expect.** Writing is off by default per unit, never happens at startup, and is never triggered by a poll, a reconnect or a restart.
- **Getting the Comfort account locked or rate-limited.** Logins are rare (tokens persisted, refresh preferred) and failed logins back off hard.
- **Credentials or tokens in git, the Nix store, logs or the UI.**
- **Home bouncing:** a value set in Home snaps back to the old one before the cloud catches up.
- **Accessories disappearing from Home** because the cloud was unreachable at boot.

## Constraints

- Cloud only: `https://app-prod.kumocloud.com/v3`, header `x-app-version: 3.2.4`, bearer tokens. Access token lifetime about 20 min; comfort_HA refreshes after 15 min. See `docs/api-inventory.md` for what is known and how confident we are.
- Host: bigthink, x86_64 NixOS unstable. `/` is an ephemeral tmpfs; only paths listed in `storage.nix` persist. Deploy workflow is in `plan.md` ("How we work").
- Go, matching sibling projects: module path `github.com/hughobrien/comfortd`, GPL-3.0-or-later.
- Library versions: newest stable releases (Hugh, 2026-11-07). Exceptions: the `go` directive in `go.mod` must not exceed the Go version of either nixpkgs that builds it, comfortd's own lock or bigthink's (its input uses `follows`; Go 1.26.10 on 2026-11-07). The `github.com/a-h/templ` module version must equal the `templ` binary version in that nixpkgs, because the gate regenerates templates and fails on drift. `flake.lock` follows current nixos-unstable and may be updated freely.
- Mac (dev machine) has no Go or nix. Use `brew install go` for local tests. Nix builds, including the gate check, run on bigthink.

## Architecture

One daemon process, `comfortd`:

```
           Kumo cloud
               ^
               | HTTPS (pkg/kumo: auth, GET raw, send-command, 429 backoff)
   +-----------+------------------------------------------+
   | poller ---> store (latest raw + parsed per unit) ---> history.jsonl
   |   ^             |            \
   |   |kick         v             v
   | control <--- web UI (/ , /ui/sse, /v1)   homekit bridge (HAP)
   |   (writes: allowlist, coalesce, pending hold)
   +------------------------------------------------------+
        state dir /var/lib/comfortd: tokens.json, discovery.json,
        aids.json, lastmode.json, history.jsonl, HAP pairing store, pin.txt
```

### Packages

| Path | Responsibility | Segment |
|---|---|---|
| `pkg/kumo` | Kumo API client: credentials file, login/refresh, token persistence, raw GETs, typed parsing, `SendCommand`, retry/backoff | 1 |
| `pkg/kumo/kumotest` | In-process fake Kumo server built on the redacted fixtures in `fixtures/` | 1 |
| `internal/store` | Latest snapshot per unit (raw JSON per endpoint + parsed device/profile), fan-out of updates | 1 |
| `internal/poller` | Discovery (with on-disk cache), the poll loop, `Kick` for fast polls after a write | 1 |
| `internal/jsondiff` | Flatten JSON to dotted paths; diff two flattenings | 2 |
| `internal/history` | Append-only JSONL of field changes and commands, read-back of recent entries | 2 |
| `internal/annotate` | Embedded annotations (field meanings + confidence) | 3 |
| `internal/homekit` | HAP bridge, HeaterCooler accessory per unit, state mapping | 4, 6 |
| `internal/control` | The only write path: allowlist, validation, coalescing, pending hold, audit | 5 |
| `cmd/comfortd` | Flags, wiring, HTTP routes, SSE push, templ UI (`ui/`) | 1+ |
| `nix/module.nix`, `flake.nix` | Package, gate check, NixOS module | 1 |

### Units

A unit is a zone with an adapter. Discovered as: `GET /sites/` → for each site `GET /sites/{id}/zones` → each zone gives `name`, `id`, `adapter.deviceSerial`. The unit's slug is its zone name lowercased with non-alphanumerics replaced by `-` (`office`, `house`). The device key is `deviceSerial` (not `serialNumber`, which is a different value). Discovery is written to `discovery.json` on success and read from it when the cloud is unreachable at startup.

### Polling

- Every `--poll-interval` (default 60 s): `GET /sites/{id}/zones` once (it carries `holdMode`, `hasActiveSchedule` for every zone), then `GET /devices/{serial}` per unit.
- At startup and every 6 h: `/profile` and `/status` per unit. `/sensor` once at startup (404 is a normal answer, stored as such).
- `Kick(slug)` (used after a write) adds extra device polls at +3 s, +8 s and +15 s.
- A unit is **stale** when its last successful device poll is older than 3× the poll interval, or the device reports `connected: false`.

### Auth and the account

- Credentials file: two lines, email then password (CR/whitespace trimmed). Re-read on every login so a password change needs no restart. Path from `--credentials`, default `$CREDENTIALS_DIRECTORY/credentials` (systemd `LoadCredential`).
- `/refresh` body shape is unverified (comfort_HA says `{access, refresh}`). The client accepts that and the `/login`-style `{token:{access, refresh}}`, and logs every refresh, so segment 1 can confirm it live.
- Tokens persisted to `tokens.json` (mode 0600): access, refresh, obtained-at. Startup order: use stored access token if younger than 15 min; else refresh; else log in.
- A 401 on an API call: refresh once, retry once; if refresh fails, log in once, retry once.
- Login failure backoff: an auth rejection (401/403 on `/login`) blocks further logins for 15 min. Other failures back off 30 s doubling to 15 min.
- 429: retry after 2, 4, 8 s, then fail that call (the next poll tries again).
- Every login is logged at info with a running count, so excess logins show up in the journal.
- Never logged or served: password, tokens, Authorization header, login response body.

### Change history

Every poll, each endpoint's new raw JSON is flattened and diffed against the previous one. Each changed path becomes one JSONL record in `history.jsonl`:

```json
{"t":"2026-11-08T07:00:03Z","unit":"office","endpoint":"device","path":"spHeat","old":"19","new":"21"}
```

`old`/`new` are the JSON encodings of the values (`null` for absent). First sight of a unit after startup records nothing (no previous value). Commands are recorded too, as `endpoint:"command"`, `path:"<field>"`, `new:<value>`, plus `"source":"ui"|"homekit"`. At 50 MB the file rotates to `history.jsonl.1` (one generation kept). This is how we expect to learn when schedules fire, what `realValues` does, and how `updatedAt` behaves.

### Web UI and JSON API

Listens on `--listen` (default `127.0.0.1:9879`); bigthink's nginx serves it as `comfortd.wg`. Stack copied from breezyd: templ components, datastar (vendored JS bundle) and server-sent events. SSE responses set `X-Accel-Buffering: no`.

| Route | Purpose | Segment |
|---|---|---|
| `GET /healthz` | 200 `ok` if every unit polled OK within 3× interval, else 503 with the stale units | 1 |
| `GET /v1/units` | JSON list: slug, name, serial, stale, last OK, last error | 1 |
| `GET /v1/units/{slug}/raw/{endpoint}` | Latest raw JSON exactly as received (`zone`, `device`, `profile`, `status`, `sensor`) | 1 |
| `GET /v1/history?unit=&limit=` | Recent history records, newest first (default 100, max 1000) | 2 |
| `GET /` | Explorer page | 2 |
| `GET /ui/sse` | Push channel | 2 |
| `GET /ui/static/...` | style.css, vendored datastar | 2 |
| `GET /homekit` | Pairing PIN and accessory IDs | 4 |
| `POST /v1/units/{slug}/command` | JSON `{"mode":"heat","spHeat":21}` (both optional) → control | 5 |
| `POST /ui/units/{slug}/...` | Datastar actions for the control buttons | 5 |

**Explorer (segment 2):** one section per unit. For each endpoint, a table of flattened path → value. Rows changed in the last 10 minutes are highlighted. Noisy paths (`updatedAt`, `lastStatusChangeAt`, `rssi`, `routerRssi`, `lastUpdated`) are dimmed but still shown. A live feed shows the latest 50 history records. Updates arrive over SSE as whole-section replacements (`#unit-{slug}`) and prepended feed rows. Datastar keeps the page itself thin.

**Annotations (segment 3):** `internal/annotate/annotations.toml`, hand-edited and embedded, takes over from the decode table in `docs/api-inventory.md`. Each entry: `path`, `label`, `confidence` (`app-confirmed`, `api-only`, `assumed`, `hypothesis`), optional `values` map (raw value → meaning) and `note`. The explorer shows label, decoded value and a confidence badge, and lists unannotated paths separately ("unknown fields") so they stand out.

**Cards (segment 3):** above the raw tables, one card per unit: mode, power, heat/cool setpoints, room temp, connected/stale, schedule active, hold enabled, last poll. Read-only until segment 5 adds controls to them.

### HomeKit (segments 4 and 6)

- Library: `github.com/brutella/hap` (upstream; keylight-hap's fork exists only for Adaptive Lighting). HAP port `--hap-port` (bigthink 9880), bridge name `comfortd`, store under the state dir, PIN in `pin.txt` generated like keylight-hap/breezyd (8 digits, not in hap's weak list), displayed as Home's `123-45-678`.
- Bridge accessory plus one `accessory.New(info, accessory.TypeAirConditioner)` per unit listed in `--homekit-units` (default empty; bigthink sets `office`). Each has a `service.NewHeaterCooler()` with `HeatingThresholdTemperature` added (`AddC`). `CoolingThresholdTemperature`, `RotationSpeed`, `SwingMode` are added only when those features are built.
- **Accessory IDs** are persisted in `aids.json` (serial → aid), assigned once starting at 2, never reused. They're set on `A.Id` before `hap.NewServer`.
- Accessories are built from discovery (live or cached), so the accessory set is stable when the cloud is down.
- **Mapping device → HomeKit** (pure function, table-tested):

| Device state | Active | CurrentHeaterCoolerState | TargetHeaterCoolerState |
|---|---|---|---|
| `power` 0 or `operationMode` off | 0 | Inactive (0) | unchanged |
| heat | 1 | Heating (2) | Heat (1) |
| cool | 1 | Cooling (3) | Cool (2) if allowed, else unchanged |
| auto | 1 | Idle (1) | Auto (0) if allowed, else unchanged |
| autoHeat / autoCool | 1 | Heating / Cooling | Auto if allowed, else unchanged |
| vent, dry | 1 | Idle (1) | unchanged |

  "Allowed" means the value is in the characteristic's `ValidVals`, which is `[Heat]` until cool/auto are probed. Heating means "in heat mode", not "compressor running" (the API doesn't expose that; comfort_HA does the same). `HeatingThresholdTemperature` = top-level `spHeat` (the requested value, so Home doesn't bounce), range from the profile's `minimumSetPoints.heat`/`maximumSetPoints.heat`, step 0.5. `CurrentTemperature` = `roomTemp`; it jumps about 2 °C on heat↔vent changes, which is expected.
- **Stale unit:** characteristic read requests return HAP status -70402 (communication failure) via `ValueRequestFunc`, which Home shows as "No Response". Segment 4 verifies this on a real iPhone; if it doesn't work, it falls back to leaving the last values and records the finding.
- **Segment 4 is read-only:** remote writes are rejected (`OnSetRemoteValue` returns an error).
- **Segment 6 writes:**
  - Active 0 → `{operationMode: off}`.
  - Active 1 → `{operationMode: <last non-off mode>}` from `lastmode.json` (default `heat`).
  - Target Heat → `{operationMode: heat}`.
  - Heating threshold → `{spHeat: v}` rounded to 0.5.
  - All go through control, so they're coalesced.

### Control (segments 5 and 6)

The only code that calls `SendCommand`.

- **Allowlist:** `--writable-units` (default empty). Anything else returns `ErrNotWritable`. bigthink sets `office` only.
- **Validation:** mode must be one of `off`, `heat`, `vent` (the probed ones; extended per feature). `spHeat` within the profile's heat min/max, rounded to 0.5. Invalid → `ErrInvalid`, nothing sent.
- **Coalescing:** intents for a unit merge (later field values win) and are sent as one multi-field command after 1 s with no new intent. `Apply` returns once the intent is queued.
- **Pending hold:** after sending, the sent fields are pending. Views (UI and HomeKit) show pending values instead of polled ones until a device poll matches every pending field, or 30 s pass. A timeout writes a history record `path:"<field>", endpoint:"unconfirmed"` and drops the hold. This replaces comfort_HA's `updatedAt` approach, which doesn't fit what we observed (`updatedAt` moves on every report).
- After sending: `poller.Kick(slug)`, and a history `command` record.
- **Last mode:** each device poll whose mode is allowed and not `off` updates `lastmode.json`, so the remembered mode is always one `Apply` accepts.
- **Change notification:** an `OnChange(slug)` callback fires on every pending/error change, so the UI and HomeKit re-render without waiting for a poll. The store calls subscribers in registration order, and the controller's `Observe` is registered first.

### Packaging and deploy

- `flake.nix` (shaped like keylight-hap's):
  - `packages.default` via `buildGoModule`, built from `cmd/comfortd`.
  - `devShells.default`: go, gopls, templ, just.
  - `checks.gate`: go vet, gofmt check, templ drift check, `go test ./...`, race tests.
  - `nixosModules.default`.
- `.forgejo/workflows/test.yml`: identical to keylight-hap's.
- NixOS module options:

| Option | Default |
|---|---|
| `services.comfortd.enable` | |
| `package` | |
| `listen` | `"127.0.0.1:9879"` |
| `credentialsFile` | required path; passed with `LoadCredential=credentials:<path>` |
| `pollInterval` | `"60s"` |
| `homekit.enable` | |
| `homekit.port` | |
| `homekit.units` | `[]` |
| `writableUnits` | `[]` |
| `openFirewall` | |

  Hardening is copied from keylight-hap's module (including `AF_NETLINK`), with `StateDirectory = "comfortd"`.
- **bigthink:**
  - `net-data.nix`: `comfortd = 9879`, `comfortdHomeKit = 9880`.
  - `network.nix`: allow `comfortdHomeKit` on the LAN.
  - `storage.nix`: `comfortd` in `libServices`.
  - New `services/comfortd.nix`: the service, and `nginx.virtualHosts."comfortd.wg" = net-lib.mkHostProxyPass "comfortd" net.lo.ipv4`.
  - `flake.nix`: input and module.
  - Credentials at `/persist/comfortd/credentials` (already in place).

## Segments

Each segment is one plan, ends deployed on bigthink, and records its findings in `plan.md`'s Log.

| # | Plan | Delivers | Hugh needed for |
|---|---|---|---|
| 1 | `2026-11-07-s1-skeleton-deploy.md` | Kumo client, fake, poller, `/v1` raw JSON, flake, module, CI, live on bigthink | nothing |
| 2 | `2026-11-07-s2-explorer.md` | Change history, explorer page with live SSE | nothing |
| 3 | `2026-11-07-s3-annotations-cards.md` | Annotations, unknown-fields list, status cards | nothing |
| 4 | `2026-11-07-s4-homekit-readonly.md` | Office in Home, read-only | pairing, checking "No Response" |
| 5 | `2026-11-07-s5-ui-controls.md` | Office controls in the web UI | agreeing the first live write |
| 6 | `2026-11-07-s6-homekit-controls.md` | Office controls in Home | schedule decision, trying it |
| 7 | written when reached | House; then one feature at a time | each probe |

Segment 6 starts by reading at least several days of history from segments 1–5. If a schedule visibly reverts changes, a schedule/hold segment goes before 6.

**Segment 7 recipe** (for each later feature):
1. Probe it on Office (agreed with Hugh), using the daemon's command endpoint with the allowlist and validation extended.
2. Update `docs/api-inventory.md` and the annotations.
3. Decide the HomeKit mapping and write a short plan in the same format.
4. Build, test, deploy, log.

House joins by adding it to `homekit.units` and then `writableUnits`, after a read-through of its fixture differences (3 fan speeds, no vane, heat 10–28).

## Left to the builder

Names of private helpers, file splits inside a package, CSS, exact log wording, test helper shapes. The pinned values above (ports, paths, defaults, timings, JSON shapes, mapping table) are not open.
