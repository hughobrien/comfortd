# comfortd — Plan

High-level, living document, maintained by the agent. Update as sessions progress: tick off slices and record findings under "Log".

## Goal

Control two Mitsubishi Comfort (Kumo Cloud) air conditioners from Apple Home, replacing the frustrating Mitsubishi app for everyday use. A small Go daemon on bigthink exposes each AC as a HomeKit accessory, with a mini web UI that starts as a raw API explorer and grows with our understanding.

## Current state

- Phase 0 (API reverse engineering) is done for the MVP scope. See `docs/api-inventory.md`.
- Office is in heat, setpoint 19 (left there by the last probe). House has not been written to.
- Nothing is built or deployed yet. **Next: design doc for slices 1–5, then slice 1.**
- **Blocker for the first deploy:** this repo is private, while `breezyd` and `keylight-hap` are public. A private flake input needs auth for rebuilds and probably for the nixos CI gate. Recommended fix is to make it public (the serials and dumps aren't in git). Waiting on Hugh.

## How we work

**Iterative deployment.** Every slice is deployed to bigthink and used before the next one starts. Deployment comes first, not last.

**One unit at a time.** Office (ductless MSZ-FX12NL) first, House (ducted PVA-AA36NL) after. Probe writes to an AC are agreed with Hugh beforehand and logged in `docs/api-inventory.md`. Findings are logged as we go.

**The agent deploys.** Hugh authorised the agent to run `nixos-rebuild` (2026-11-07). The loop:

1. On the Mac, run tests with brew Go (`brew install go`; there is no Go or nix locally).
2. Check the gate before pushing: rsync to bigthink and `nix build "path:$PWD#checks.x86_64-linux.gate"` with `TMPDIR` set to a directory that exists. `/` is an 8 GB tmpfs, and piping through `tail` hides the exit code.
3. Push to `main`; the Forgejo gate must pass.
4. In `~/.config/nixos` on bigthink, run `nix flake update comfortd` (this input only, so nixpkgs doesn't move). Commit.
5. Run `sudo diff -r -x .git -x .claude -x .forgejo ~/.config/nixos /etc/nixos`. Hotfixes can live only in `/etc`; port them back first. If the diff shows anything unrelated, stop and ask.
6. `sudo cp` the changed files to `/etc/nixos`, then rebuild (`doas nixos-rebuild switch`).
7. Check the service and the UI. Only a reboot fully proves persistence and sandboxing, because `/` is ephemeral.

## bigthink integration

Host: x86_64, NixOS unstable, already running `breezyd` and `keylight-hap` from flake inputs. Everything below is in `~/.config/nixos`.

- `flake.nix`: add the `comfortd` input (`git+https://forgejo.wg/hughobrien/comfortd`, nixpkgs follows) and `comfortd.nixosModules.default`.
- `services/homekit.nix`: enable the service, and add the nginx vhost `comfortd.wg` via `net-lib.mkHostProxyPass`. The UI binds to loopback, as `breezyd` does.
- `net-data.nix`: add a UI port and a HomeKit port (9876–9878 are taken; confirm the next ones are free).
- `network.nix`: open the HomeKit port on the LAN interface. mDNS is already handled there.
- `storage.nix`: add `comfortd` to `libServices`, or a reboot loses the HomeKit pairing and tokens.
- **Credentials: done.** `/persist/comfortd/credentials` (root, mode 600, two lines: email, then password). The service reads it with `LoadCredential`, as invidious does. The module takes a `credentialsFile` option, so nothing goes in the Nix store.
- Module hardening: copy keylight-hap's. `AF_NETLINK` is required, or mDNS silently advertises on no interfaces. Use `StateDirectory`, so paths exist on a cold boot.

## Slices

### 1. Read-only daemon, deployed

- [ ] Go module following breezyd's layout: `pkg/` Kumo client, `cmd/comfortd/ui/` (templ + datastar, `PushHub`, `/ui/sse`), `/v1` JSON.
- [ ] Login, refresh and 429 backoff. **Persist the refresh token** in the state directory, so frequent deploys don't mean frequent logins (unknown whether those risk a lockout or disturb the app's session).
- [ ] Poll about every 60 s: device state often, profile rarely.
- [ ] Raw explorer: latest unmodified JSON per endpoint per unit, with changed fields highlighted between polls.
- [ ] **Change history:** append every field change to a JSONL file in the state directory. This answers open questions with no write probes: when schedules fire, `realValues`, `updatedAt`, token lifetime.
- [ ] Fake Kumo server (needed from the start: the gate runs in a sandbox with no network).
- [ ] Nix flake, NixOS module, Forgejo gate (including templ drift check), bigthink integration above.
- [ ] No HomeKit and no write path in this slice.

### 2. Understanding in the UI

- [ ] Annotations file (field/value -> meaning + confidence, seeded from the decode table), so known fields get labels and unknown ones stay raw.
- [ ] Read-only cards per unit: mode, setpoints, room temp, connected.

### 3. HomeKit, Office read-only

- [ ] Bridge with one HeaterCooler for Office: current temp, state, setpoint. PIN and pairing persisted.
- [ ] Stable accessory IDs derived from serials, so adding House later doesn't disturb pairing.
- [ ] **Cache discovery on disk**, so a cloud outage at boot doesn't remove accessories from Home. Show "No Response" and recover on its own.
- [ ] Reboot bigthink once to prove the pairing survives.

### 4. Controls in the web UI, Office

- [ ] Off, heat, heat setpoint: all verified by probes. One write path, shared later with HomeKit.
- [ ] **Per-unit write allowlist, read-only by default; House stays off it.** Never write at startup.
- [ ] Pending-value hold: show the requested value until a read matches or a timeout passes, and poll faster after a write. This replaces comfort_HA's `updatedAt` reconciliation: we saw about 5 s of lag, not a minute, and `updatedAt` changes on every report.

### 5. Controls in Home, Office

- [ ] Before starting, read the slice 1 history to see whether schedules revert changes. If they do, handling them moves up to here.
- [ ] Active and heating threshold. **Offer only Heat in Home until cool and auto are probed.**
- [ ] Active on needs a mode to send (a mode alone turns the unit on): persist the last mode used.
- [ ] Combine bursts of Home writes over about 1 s into one multi-field command (confirmed to work).
- [ ] Fan-only or dry set elsewhere shows as Active + Idle in Home.

### 6. House, then features

- [ ] Repeat for House (3 fan speeds, no vane, reports humidity).
- [ ] Pick from, one at a time: cool and auto (after probes), fan speed, swing/vane, dry, humidity, filter status, schedule/hold. Each gets a mapping decision, implementation, test, README note.

### Throughout

- Log hygiene: never tokens or passwords.
- README grows with scope, limitations, setup; complete by slice 5.
- Behaviour on API outage, token expiry and password change is handled as each slice meets it, not saved for the end.

## Deferred probes

Do when the matching feature is built. Agree with Hugh first, log in `docs/api-inventory.md`, use `spike/probe_write.py '<json>'` until the daemon can write.

- `auto`: band 19–26; narrow band (22–23); inverted band (`spHeat` > `spCool`). Learn whether a minimum gap is enforced. Until then the bridge enforces 1 °C (mirrors comfort_HA).
- `cool` and `dry` (behaviour, setpoint use; `usesSetPointInDryMode` is true).
- `power` written alone (not needed: a mode turns the unit on, `off` turns it off).
- `fanSpeed` and `airDirection` writes, and comfort_HA's fan labels against Office's 5 speeds.
- Can a hold be set through the API?
- Read comfort_HA's `hvac_action` (idle vs heating/cooling) in depth, needed for HomeKit's current state in slice 3.
- All of the above again for House.

Dropped for now: a LAN/local API check and capturing Comfort app traffic. Neither is needed for the MVP; revisit if the cloud API proves unreliable or a wanted feature isn't visible.

## Key constraints

- Cloud-only: a Mitsubishi outage or no internet means no control.
- Unofficial API: `x-app-version` may need bumping; 429s need backoff.
- Celsius end to end, 0.5 °C steps. Home displays per phone locale.

## Known API surface

See `docs/api-inventory.md`. Base `https://app-prod.kumocloud.com/v3`, header `x-app-version: 3.2.4`, `POST /devices/send-command` with `{deviceSerial, commands:{...}}`.

## References

- `../breezyd`: closest structural model (daemon, `pkg/`, `/v1`, templ + datastar UI, in-process HAP, fake device, justfile, Nix module, snapshot-and-diff reverse engineering).
- `../keylight-hap`: smallest HAP + Nix module + gate example.
- `../btplayer`: reference for keeping the page small.
- `../comfort_HA`: prior reverse engineering. Verify, don't trust.

## Decisions

- Daemon, UI and packaging follow breezyd and keylight-hap (agent's call). UI is templ + datastar from the start (Hugh).
- HomeKit service is **HeaterCooler** (agent's recommendation, Hugh leaning the same way): Active is separate from mode; separate thresholds match `spHeat`/`spCool`; fan and swing slots exist for later. Cost: no home for fan-only or dry, which stay in the web UI.
- Reverse engineering continues in the open through the explorer and annotations.
- Schedule/hold implementation is later work (Hugh). Observing it starts in slice 1 (agent's call).
- Iterative deployment to bigthink (Hugh); agent runs rebuilds (Hugh).
- Credentials as a file under `/persist`, not plain text in the config (agent's call: this is an internet account, unlike the LAN-only secrets accepted elsewhere on bigthink).

## Open questions

- Will cloud schedules overwrite Home changes? Both zones have one (`hasActiveSchedule`, `holdMode`). Biggest unknown for real use; slice 1 history should answer it.
- Does logging in again affect the app's session, and do refresh tokens rotate? Slice 1.
- Repo visibility (see Current state).
- API details: `docs/api-inventory.md`.

## Log

- 2026-11-07: Read comfort_HA and keylight-hap, wrote this plan.
- 2026-11-07: Ran read-only spike (`spike/dump.py`). Login and all GETs work (sensor 404 on both, expected). Two zones: Office (MSZ-FX12NL ductless), House (PVA-AA36NL ducted). Wrote `docs/api-inventory.md`.
- 2026-11-07: Write probes on Office (`spike/probe_write.py`): `spHeat: 21`, `operationMode: vent`, `operationMode: off`, then heat/19 in one multi-field command. All accepted, visible within about 5 s, app agreed. A mode alone turns the unit on; `off` also sets `power` 0. `spHeat` is the real setpoint.
- 2026-11-07: Hugh deferred the auto, cool and dry probes. Repo created as `comfortd` on Forgejo (private).
- 2026-11-07: Reviewed the plan for iterative deployment to bigthink and reordered it into deployable slices. Checked bigthink's config conventions. Moved the Comfort credentials from `/tmp/comfort` to `/persist/comfortd/credentials`; temp copy removed. Hugh authorised the agent to run rebuilds.
