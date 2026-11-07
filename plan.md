# comfortd — Plan

(Repo renamed from the working name "comfort-hap" to `comfortd`; the daemon is `comfortd`.)

High-level, living document. Update as sessions progress; tick off phases and record findings under "Log".

## Goal

Control two Mitsubishi Comfort (Kumo Cloud) air conditioners from Apple Home, replacing the frustrating Mitsubishi app for everyday use. A small Go daemon exposing each AC as a HomeKit accessory, following the same pattern as `../keylight-hap`.

## Starting points

- `../keylight-hap` — pattern to copy: Go, `brutella/hap`, persisted PIN + pairing store, Nix flake + NixOS module, Forgejo CI gate, `docs/superpowers/{specs,plans}`.
- `../comfort_HA` — prior reverse engineering of the Kumo Cloud V3 API (`custom_components/kumo_cloud/{api,coordinator,climate}.py`). Source of endpoints, mode/fan/vane value mappings, and the command-cache/anti-bounce approach.
- `../breezyd` — closest structural model: one Go daemon owning all device traffic + poller/cache, protocol lib in `pkg/`, JSON API + `/ui` dashboard (templ + datastar over SSE), in-process HAP bridge, fake device for tests, `SPECIFICATION-*.md`, justfile, Nix module. Its reverse-engineering method (`tools/snapshot.py` capture + diff while changing one setting in the vendor app) is the model for our explorer.
- `../btplayer` — minimal web page model: stdlib server + one `index.html`, `/api/state` + `/api/set`, no build step.

## Known API surface (from comfort_HA; verify, don't trust)

Base `https://app-prod.kumocloud.com/v3`, header `x-app-version` (comfort_HA uses 3.2.4).

- `POST /login`, `POST /refresh` — tokens (access ~20 min)
- `GET /accounts/me`, `GET /sites/`, `GET /sites/{id}/zones`
- `GET /devices/{serial}`, `/profile`, `/status`, `/sensor`
- `GET /zones/{id}/notification-preferences`
- `POST /devices/send-command` — `{deviceSerial, commands:{...}}`
- Command/state fields seen: `power`, `operationMode`, `spCool`, `spHeat`, `fanSpeed`, `airDirection`, `roomTemp`, `humidity`, `updatedAt`

## Key risks / design constraints

- Cloud-only; Mitsubishi outage or no internet means no control. A LAN path may exist for some adapters but is unverified.
- Cloud can take up to ~1 min to reflect commands -> need a command cache with `updatedAt` reconciliation so Home doesn't bounce back (port from comfort_HA).
- Unofficial API: version header may need bumping; rate limits (429) need backoff; poll ~60 s.
- Credentials: Comfort login must be supplied via secret file / systemd credential, never in the Nix store or logs.
- HomeKit is Celsius-only and the API uses 0.5 °C steps, so the Fahrenheit lookup tables from comfort_HA are not needed.

## Phases

### 0. API reverse engineering and feature inventory (do first)

- [ ] Throwaway spike (Go or script, clearly labelled throwaway): log in (password from env var, never written to disk) and dump raw JSON for sites, zones, device detail, profile, status, sensor for both units. Store dumps outside git or with serials/tokens redacted.
- [ ] Write `docs/api-inventory.md`: every endpoint, field and writable command found, with type/range/enum values, which are read-only vs writable, and per-unit differences (heat support, temp range, fan speeds, vanes).
- [ ] Fill gaps comfort_HA doesn't cover: inspect the `/profile` capabilities, try additional read-only endpoints, and (if needed) capture app traffic from the Comfort app (e.g. mitmproxy) to find other commands (schedules, eco/economy, presets, humidity, vane left/right, filter reset, etc.).
- [ ] Check whether a LAN/local API is usable for these adapters (low priority; note result either way).
- [ ] Mark each inventoried feature with a HomeKit mapping candidate and a build priority.

### 1. Design doc

- [ ] Pick scope from the inventory; write `docs/superpowers/specs/<date>-comfort-hap-design.md` (same format as keylight-hap).
- [x] HomeKit service: **HeaterCooler** (decided, see Decisions).
- [ ] Web UI design: raw explorer first (see Phase 2).

### 2. Minimal build (MVP)

- [ ] Kumo client: login/refresh, sites/zones discovery, get state, send command, 429 backoff.
- [ ] Command cache / anti-bounce.
- [ ] One HomeKit accessory per AC: on/off, mode, target temperature, current temperature.
- [ ] Polling loop + two-way sync; PIN + pairing persistence.
- [ ] Fake Kumo server for tests (cf. keylight-hap `fakedevice.go`).
- [ ] Mini web UI, grown alongside the HomeKit features (layout of breezyd, simplicity of btplayer):
  - Step 1, raw explorer: latest unmodified JSON per endpoint per unit; changed fields highlighted between polls (live version of the snapshot-and-diff technique, usable while changing things in the Comfort app); a hand-edited annotations file (field/value -> meaning + confidence, seeded from `docs/api-inventory.md` decode table) so known fields show friendly labels and unknown ones stay raw.
  - Step 2: read-only friendly cards per unit (mode, setpoints, room temp, connected).
  - Step 3: controls (power/mode/setpoint) via the same code path HomeKit uses.
  - Stack: breezyd's templ + datastar over SSE from the start (Hugh's call, 2026-11-07: the pattern is proven). Reuse breezyd's layout: `cmd/comfortd/ui/` (templates, view, style.css, vendored datastar), `PushHub`, `/ui/sse`, `/v1` JSON API. btplayer remains the reference for keeping the page itself small.
- [ ] Nix flake, NixOS module with credential file, CI gate.

### 3. Incremental features (pick from the inventory, one at a time)

Candidates, in rough order: fan speed, swing/vane, dry mode, humidity, wireless sensor, filter-dirty status, schedules/other app features. Each gets: mapping decision, implementation, test, README note.

### 4. Hardening

- [ ] Behaviour on API outage / token expiry / password change (accessory shows "No Response", recovers on its own).
- [ ] Log hygiene (no tokens/passwords).
- [ ] README with scope, limitations, setup.

## Decisions so far

- Follow keylight-hap's stack and layout (my call).
- Start minimal; the full feature inventory drives later additions.
- Celsius end to end (my call); Home displays per phone locale.
- HomeKit service: **HeaterCooler** (my recommendation, Hugh was leaning the same way). Reasons: Active is separate from mode (matches `power`/`operationMode`); separate heating/cooling thresholds match `spHeat`/`spCool`; fan speed and swing slots exist for later. Cost: no place for fan-only/dry in HomeKit; those show as active/idle in Home and are controlled from the web UI.
- Reverse engineering continues in the open: the explorer UI shows raw API responses, and understanding is added incrementally through the annotations file.
- Cloud schedule/hold handling is later work (Hugh, 2026-11-07).

## Working order

One unit at a time. **Office (ductless MSZ-FX12NL) first**, House (ducted PVA-AA36NL) after. Any write to an AC is agreed with Hugh beforehand and tested on Office only.

## Deferred probes (to be implemented, Office first)

Not needed to start the MVP; do when the matching feature is built. Each probe: agree with Hugh first, log it in `docs/api-inventory.md`, use `spike/probe_write.py '<json>'`.

- `auto` mode: switch with band 19–26; narrow band (e.g. 22–23); inverted band (`spHeat` > `spCool`). Learn whether a minimum gap is enforced. Until then the bridge enforces a 1 °C minimum gap itself (my call, mirrors comfort_HA).
- `cool` and `dry` modes (behaviour, setpoint use; `usesSetPointInDryMode` is true).
- `power` written on its own (not needed for MVP: a mode alone turns the unit on, `off` turns it off).
- `fanSpeed` and `airDirection` writes, and the fan-label tables from comfort_HA against the Office's 5 speeds.
- `realValues` semantics (hypothesis: unit's own value while it differs from the requested one).
- Cloud schedule/hold behaviour: does it revert Home changes; can a hold be set.
- Read comfort_HA `hvac_action` (idle vs heating/cooling) and command-cache code in depth.
- Everything above for the House (ducted) unit afterwards.

## Open questions

- Both zones have an active cloud schedule (`hasActiveSchedule`, `holdMode`). Will it overwrite Home-initiated changes? Can a hold be set via the API? (biggest unknown for real-world use)
- See `docs/api-inventory.md` for the detailed list of API unknowns.

- Do both units support heat and cool, or cool only? (answered by phase 0 dumps)
- Is a local/LAN control path viable? (phase 0, optional)
- What HeaterCooler should show when the unit is in fan-only or dry (Active + Idle is the working idea).

## Log

- 2026-11-07: Read comfort_HA and keylight-hap, wrote this plan. Next: phase 0 spike (needs Comfort credentials via env var).
- 2026-11-07: Ran read-only spike (`spike/dump.py`, creds in gitignored `./comfort`). Login and all GETs work (sensor 404 on both, expected). Two zones: Office (MSZ-FX12NL ductless), House (PVA-AA36NL ducted). Wrote `docs/api-inventory.md` for Office. Next: agree one harmless write probe on Office.
- 2026-11-07: Write probe on Office (`spike/probe_write.py`): `send-command {spHeat:21}` -> 200 `{"devices":[serial]}`; state showed 21 within 5 s and the app agreed. `spHeat` is the real setpoint (`realValues` is something transient). Office heat setpoint left at 21 at Hugh's request. Next: probe `operationMode`/`power`, then check whether the schedule reverts the change.
- 2026-11-07: Office set to fan only (`operationMode: vent`) at Hugh's request; accepted, state visible in 5 s, app agreed. `roomTemp` jumped 20.5 -> 22.5 on the mode change. Office is currently in vent mode, heat setpoint 21. Next: `off`, `cool`, `auto`, and `power`, one at a time.
- 2026-11-07: Office `operationMode: off` -> `power` 0 as well, app agreed. Office is currently OFF. Next: how to turn back on (`power:1` vs a mode), then `cool`, `auto`, `dry`; restore heat/21 at the end.
- 2026-11-07: Office set to heat/19 in one multi-field command: accepted; a mode alone turns the unit on (no `power` write needed). `realValues` now looks like the unit's own value while it differs from the requested one (hypothesis). Office is currently heat, setpoint 19. Remaining modes to probe: `cool`, `auto`, `dry`.
- 2026-11-07: Hugh deferred the auto/cool/dry probes; moved to "Deferred probes". Office left in heat, setpoint 19. Phase 0 is effectively done for the MVP scope (power/mode/setpoint/room temp); next is phase 1 (design doc).
