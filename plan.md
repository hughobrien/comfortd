# comfortd — Plan

High-level, living document, maintained by the agent. Update as sessions progress: tick off segments and record findings under "Log". Agents: read `CLAUDE.md` first.

## Goal

Control two Mitsubishi Comfort (Kumo Cloud) air conditioners from Apple Home, replacing the frustrating Mitsubishi app for everyday use. A small Go daemon on bigthink exposes each AC as a HomeKit accessory, with a mini web UI that starts as a raw API explorer and grows with our understanding.

## Current state

- Phase 0 (API reverse engineering) is done for the MVP scope. See `docs/api-inventory.md`.
- Office is in heat, setpoint 19 (left there by the last probe). House has not been written to.
- Segment 1 is deployed: comfortd polls both units and serves raw JSON at https://comfortd.wg/. Segment 2 is deployed too: the same page is a live explorer with a change feed, and changes land in `history.jsonl`. Segment 3 is deployed: annotated rows, an unknown-fields list and a status card per unit. Segment 4 is deployed: Office is paired in Apple Home, read-only (reboot check skipped). Segment 5 and 6 are deployed: Office is controllable from the web UI and from Apple Home (verified live). **Next: segment 7 (House, then features).**
- Repo is public (Hugh, 2026-11-07), like `breezyd` and `keylight-hap`, so the flake input needs no auth. Keep serials, MACs, dumps and credentials out of git.

## Segments

Design: `docs/superpowers/specs/2026-11-07-comfortd-design.md`. Each segment is one plan in `docs/superpowers/plans/`, ends deployed on bigthink, and gets a Log line. Execute them in order; a later plan may need small adjustments if an earlier segment's interfaces drifted (its Interfaces blocks say what it expects).

| # | Plan | Delivers | Hugh needed for | Status |
|---|---|---|---|---|
| 1 | `2026-11-07-s1-skeleton-deploy.md` | Kumo client, fake server, poller, `/v1` raw JSON, flake, module, CI, live on bigthink | nothing | [x] |
| 2 | `2026-11-07-s2-explorer.md` | Change history, live explorer page | nothing | [x] |
| 3 | `2026-11-07-s3-annotations-cards.md` | Field annotations, unknown-fields list, status cards | nothing | [x] |
| 4 | `2026-11-07-s4-homekit-readonly.md` | Office in Home, read-only; reboot check | pairing, "No Response" check, reboot slot | [ ] |
| 5 | `2026-11-07-s5-ui-controls.md` | Office controls in the web UI | agreeing the first live write | [ ] |
| 6 | `2026-11-07-s6-homekit-controls.md` | Office controls in Home (MVP done) | schedule decision, trying it | [ ] |
| 7 | written when reached (recipe in the spec) | House; then one feature at a time | each probe | [ ] |

Segments 1–3 can run back to back without Hugh. Segment 6 starts by reading the history gathered since segment 2; if cloud schedules revert changes, a schedule/hold segment goes first.

## How we work

**Iterative deployment.** Every segment is deployed to bigthink and used before the next one starts.

**One unit at a time.** Office (ductless MSZ-FX12NL) first, House (ducted PVA-AA36NL) after. Probe writes to an AC are agreed with Hugh beforehand and logged in `docs/api-inventory.md`. Findings are logged as we go.

**The agent deploys.** Hugh authorised the agent to run `nixos-rebuild` (2026-11-07). The loop:

1. On the Mac, run tests with brew Go (`brew install go`; there is no Go or nix locally).
2. Check the gate before pushing: rsync to bigthink and `nix build "path:$PWD#checks.x86_64-linux.gate"` with `TMPDIR` set to a directory that exists. `/` is an 8 GB tmpfs, and piping through `tail` hides the exit code.
3. Push to `main`; the Forgejo gate must pass.
4. In `~/.config/nixos` on bigthink, run `nix flake update comfortd` (this input only, so nixpkgs doesn't move). Commit.
5. Run `sudo diff -r -x .git -x .claude -x .forgejo ~/.config/nixos /etc/nixos`. Hotfixes can live only in `/etc`; port them back first. If the diff shows anything unrelated, stop and ask.
6. `sudo cp` the changed files to `/etc/nixos`, then rebuild (`doas nixos-rebuild switch`).
7. Check the service and the UI. Only a reboot fully proves persistence and sandboxing, because `/` is ephemeral; rebooting needs Hugh's OK.

comfortd's own `flake.lock` follows current nixos-unstable and may be updated freely (Hugh). Libraries: newest stable, with the `go` directive and templ version following that nixpkgs (see the spec).

## bigthink integration

Host: x86_64, NixOS unstable, already running `breezyd` and `keylight-hap` from flake inputs. Everything below is in `~/.config/nixos` (exact edits in segment 1 Task 8 and segment 4 Task 5).

- `flake.nix`: the `comfortd` input and module.
- `services/comfortd.nix` (new): the service, plus the nginx vhost `comfortd.wg` via `net-lib.mkHostProxyPass` (its DNS name is generated from the vhost list).
- `net-data.nix`: `comfortd = 9879`, `comfortdHomeKit = 9880` (checked free 2026-11-07).
- `network.nix`: `comfortdHomeKit` on the LAN interface (segment 4).
- `storage.nix`: `comfortd` in `libServices`.
- **Credentials: done.** `/persist/comfortd/credentials` (root, mode 600, two lines: email, then password), read through `LoadCredential`.

## Deferred probes

Do when the matching feature is built. Agree with Hugh first, log in `docs/api-inventory.md`, use the daemon's command endpoint once segment 5 exists (before that, `spike/probe_write.py '<json>'`).

- `auto`: band 19–26; narrow band (22–23); inverted band (`spHeat` > `spCool`). Learn whether a minimum gap is enforced. Until then the bridge enforces 1 °C (mirrors comfort_HA).
- `cool` and `dry` (behaviour, setpoint use; `usesSetPointInDryMode` is true).
- `power` written alone (not needed: a mode turns the unit on, `off` turns it off).
- `fanSpeed` and `airDirection` writes, and comfort_HA's fan labels against Office's 5 speeds.
- Can a hold be set through the API?
- All of the above again for House.

Dropped for now: a LAN/local API check and capturing Comfort app traffic. Neither is needed for the MVP; revisit if the cloud API proves unreliable or a wanted feature isn't visible.

## Key constraints

- Cloud-only: a Mitsubishi outage or no internet means no control.
- Unofficial API: `x-app-version` may need bumping; 429s need backoff.
- Celsius end to end, 0.5 °C steps. Home displays per phone locale.

## References

- `../breezyd`: closest structural model (daemon, `pkg/`, `/v1`, templ + datastar UI, in-process HAP, fake device, justfile, Nix module, snapshot-and-diff reverse engineering).
- `../keylight-hap`: smallest HAP + Nix module + gate example.
- `../btplayer`: reference for keeping the page small.
- `../comfort_HA`: prior reverse engineering. Verify, don't trust. Its `hvac_action` is mode-derived (heat mode = "heating"), which we copy.

## Decisions

- Daemon, UI and packaging follow breezyd and keylight-hap (agent's call). UI is templ + datastar from the start (Hugh).
- HomeKit service is **HeaterCooler** (agent's recommendation, Hugh leaning the same way): Active is separate from mode; separate thresholds match `spHeat`/`spCool`; fan and swing slots exist for later. Cost: no home for fan-only or dry, which stay in the web UI.
- Reverse engineering continues in the open through the explorer, the change history and the annotations file.
- Schedule/hold implementation is later work (Hugh). Observing it starts in segment 2 (agent's call).
- Iterative deployment to bigthink (Hugh); agent runs rebuilds (Hugh).
- Credentials as a file under `/persist`, not plain text in the config (agent's call: this is an internet account, unlike the LAN-only secrets accepted elsewhere on bigthink).
- Design owned by the agent (Hugh, 2026-11-07); see the spec for the agent's calls.

## Open questions

- Will cloud schedules overwrite Home changes? Both zones have one (`hasActiveSchedule`, `holdMode`). Biggest unknown for real use; segment 2's history should answer it before segment 6.
- Does logging in again affect the app's session, and do refresh tokens rotate? App session: no effect, Hugh's phone app stayed logged in after the daemon's login (2026-11-07). Refresh: works live (flat body, rotated token persisted, no fallback login).
- ~~Does Home show "No Response" when reads return -70402?~~ Yes (segment 4).
- API details: `docs/api-inventory.md`.

## Log

- 2026-11-07: Read comfort_HA and keylight-hap, wrote this plan.
- 2026-11-07: Ran read-only spike (`spike/dump.py`). Login and all GETs work (sensor 404 on both, expected). Two zones: Office (MSZ-FX12NL ductless), House (PVA-AA36NL ducted). Wrote `docs/api-inventory.md`.
- 2026-11-07: Write probes on Office (`spike/probe_write.py`): `spHeat: 21`, `operationMode: vent`, `operationMode: off`, then heat/19 in one multi-field command. All accepted, visible within about 5 s, app agreed. A mode alone turns the unit on; `off` also sets `power` 0. `spHeat` is the real setpoint.
- 2026-11-07: Hugh deferred the auto, cool and dry probes. Repo created as `comfortd` on Forgejo (private).
- 2026-11-07: Reviewed the plan for iterative deployment to bigthink and reordered it into deployable slices. Checked bigthink's config conventions. Moved the Comfort credentials from `/tmp/comfort` to `/persist/comfortd/credentials`; temp copy removed. Hugh authorised the agent to run rebuilds.
- 2026-11-07: Repo made public (history rescanned first: no secrets, serials or dumps).
- 2026-11-07: Wrote the design (`docs/superpowers/specs/2026-11-07-comfortd-design.md`), six segment plans, and `CLAUDE.md` for executing agents. Committed redacted fixtures of every real API response (`pkg/kumo/kumotest/fixtures`, generator `spike/redact_fixtures.py`). Found that `serialNumber` differs from `deviceSerial`; the latter is the device key.
- 2026-11-07: Segment 1 done and deployed to bigthink. Gate green locally and on Forgejo; flake is x86_64-linux only. Live: 2 units, one login, restart reused `tokens.json` (no login), first refresh at about 15 min worked (flat body, rotation persisted). Phone app stayed logged in. Surprise: the first rebuild added a TLS vhost whose cert (`/etc/nginx/comfortd.{crt,key}`, private CA) didn't exist, so nginx failed to start for about a minute; vhost removed, then re-added as plain HTTP. dnscrypt-proxy needed a manual restart to pick up `comfortd.wg`. TODO (Hugh): issue the cert, then switch `services/comfortd.nix` to `net-lib.mkHostProxyPass`. Next time: add a new vhost only after its cert exists.
- 2026-11-07: Segment 2 done and deployed. `internal/jsondiff`, `internal/history` (JSONL, 50 MB rotation), recorder, `/v1/history`, templ + datastar explorer (`/`, `/ui/sse`). Gate green on bigthink and Forgejo; datastar 1.0.1 vendored with datastar-go 1.2.2, templ 0.3.1020. Live over plain HTTP: SSE streams through nginx, history.jsonl is 0600, first records were `rssi` jitter on both units (noisy, dimmed in the UI). Schedule/`realValues` behaviour: nothing seen yet, check the history over the coming hours.
- 2026-11-07: Segment 3 done and deployed. `internal/annotate` (embedded `annotations.toml`, now the source of truth; the inventory decode table points to it), annotated rows with confidence badges, unknown-fields list, status cards. Live cards: Office Heat, 19.5 °C room, heat to 19, schedule active; House Heat, 20 °C room, heat to 20, humidity 54. From now on, update `annotations.toml` in the same commit as a finding's Log line.
- 2026-11-07: Hugh issued the cert; installed to `/etc/nginx/comfortd.{crt,key}` and switched `services/comfortd.nix` to `net-lib.mkHostProxyPass`. Rebuild changed only that file; https://comfortd.wg/healthz returns 200.
- 2026-11-07: Segment 4 deployed (Office in Apple Home, read-only; port 9880 open on the LAN). Paired by Hugh. "No Response" check done with `-force-stale office`: Home shows "No Response", so the -70402 approach works; flag removed and redeployed. hap v0.0.35: `Float.SetValue` clamps to min/max, so the threshold range is widened before setting the value. Still open: Hugh to confirm values match the Comfort app and that a setpoint write from Home is refused; reboot check (needs Hugh's OK). Observed: the `sensor` endpoint returns "not found" for both units at startup (logged as a warning each poll).
- 2026-11-07: Segment 4 checks finished. Home shows 20.0 and heating to 19.0, matching the Comfort app; a setpoint write from Home was refused and the cloud `spHeat` stayed 19. The reboot check was skipped at Hugh's request: not verified that pairing survives a reboot. State is on the `/persist` bind mount (`/var/lib/comfortd`), so it should.
- 2026-11-07: Segment 5 code deployed, no live write yet. `internal/control` (allowlist, validation, 1 s coalescing, pending hold, history `command`/`unconfirmed` records, `lastmode.json`), `POST /v1/units/{slug}/command`, `/ui/units/{slug}/mode|spheat`, Office card buttons. bigthink sets `writableUnits = [ "office" ]`. Checked: House command is 403, wrong content type 415, UI POST without the Datastar header 403, Office `spHeat` still 19. First live write waits for Hugh's yes.
- 2026-11-07: First live write through comfortd (Hugh pressed + in the UI): Office `spHeat` 19 -> 19.5, one `command` record (source `ui`), confirmed by the poll about 3 s later, no `unconfirmed` record, app showed 19.5. Left at 19.5 at Hugh's request. Fixed along the way: pending badge rendered as literal text (templ component calls need their own line); a racy control test. Segment 5 done.
- 2026-11-07: Segment 6 Task 0 (schedule evidence): history covers only about 2 h (22:30 to 00:30 UTC). The only mode, setpoint or hold changes in it are our own `spHeat` command and its confirmation; nothing unexplained, but also too short to say whether the cloud schedule overwrites changes (Office reports an active schedule). Not conclusive. The `unconfirmed` history record will show it if a schedule reverts a change. Segment 6 code written: Home writes go through `control` (Active, Target Heat, heating threshold), `Sync` reads the overlay. Not deployed yet.
- 2026-11-07: Segment 6 deployed and tried from Apple Home by Hugh (Office). Off, on, a second off/on, and a setpoint change to 20 all produced `command` records with source `homekit`, each confirmed by the next device poll within about 3 s, and no `unconfirmed` records. On from off went to heat (last mode). Office left at heat, 20. Siri was not distinguished from the Home slider in the history (both are HAP writes). Office MVP is done. Open: whether the cloud schedule overwrites changes (watch for `unconfirmed` records over a few days). Next: segment 7 (House, then features).
- 2026-11-07: Segment 7 started on Office. Fan speed and vane controls built (control intents `fanSpeed`/`airDirection`, API fields, segmented buttons on the card); the UI was reworked after breezyd (power toggle, segmented mode/fan/vane, setpoint slider limited by the status endpoint's min/max, model image, yellow `displayConfig` flags, status details collapsed, House shown as an inactive placeholder via `inactiveUnits`). Hugh pressed Powerful in the UI: `command fanSpeed superPowerful` confirmed in about 3 s (the page showed nothing because its event stream died on my redeploy; it now retries). All five fan names confirmed by Hugh setting each in the app and our poll reading it back (`superQuiet` Quiet, `quiet` Low, `low` Medium, `powerful` High, `superPowerful` Powerful). Not yet probed: vane positions, cool/dry/auto, HomeKit Rotation Speed / Swing. House fan mapping unknown.
- 2026-11-07: Vane probe on Office, Hugh said "you drive": sent swing, vertical, midvertical, midpoint, midhorizontal, horizontal, auto in turn through the command endpoint; each confirmed by the poll in 2-6 s with no `unconfirmed` records. Then fan and vane restored to auto. Fan and vane values are now fully verified on Office. Next: HomeKit mapping for fan (Rotation Speed) and swing, then the cool/dry/auto probes (these run the compressor in a new mode, so ask Hugh first).
- 2026-11-07: Fan and swing in Home (Rotation Speed 20-100 in five steps, Swing Mode = vane `swing`/`auto`): Hugh raised the fan and enabled oscillate from Home; `command fanSpeed powerful` and `command airDirection swing` (source `homekit`) each confirmed in ~2.4 s. Dry and cool probed at Hugh's request: `dry`, then `cool` + `spCool 21` as one command, both confirmed in ~6 s. `control` now allows modes off/heat/vent/dry/cool and an `spCool` intent (API and controller only; UI and Home still offer heat/vent). Office left in cool at 21 as asked. Next: `auto` probe, then decide the cool/dry/auto UI and Home mapping, then House.
- 2026-11-07: Auto observed (Hugh set it in the app, twice): mode `autoCool`, dual setpoints, our setpoint command accepted in that mode. Details in api-inventory. Not yet sent by us: `operationMode: auto`, narrow/inverted bands.
- 2026-11-07: Auto minimum gap observed: 1.5 degrees, enforced by the unit, which moves the other setpoint (api-inventory). Office left in autoCool, band 17-18.5, pending Hugh.
- 2026-11-07: UI updated with the probe findings and deployed: mode buttons Heat/Cool/Dry/Auto/Fan only (offered from the profile; autoCool/autoHeat light Auto), setpoint sliders follow the mode (heat: heat; cool: cool; auto: both; dry and fan only: none, since dry/vent setpoint use is unknown), slider limits from the status endpoint, auto holds the 1.5 gap in `control` (a lone setpoint pushes the other, as the unit does; a pair under 1.5 is refused), auto confirms when the unit reports autoCool/autoHeat. `operationMode: auto` itself has not been sent by us yet. Home still offers heat only.
