# Kumo Cloud API inventory

Living reference for what the Kumo Cloud V3 API exposes. Findings are tagged:
**[observed]** seen in our own dumps (`spike/dump.py`, 2026-11-07), **[comfort_HA]** taken from
`../comfort_HA` and not yet independently verified, **[untested]** unknown.

Scope order: **Office (ductless MSZ-FX12NL) first**, then House (ducted PVA-AA36NL). Serials, MACs
and tokens are deliberately omitted from this file.

Base `https://app-prod.kumocloud.com/v3`, header `x-app-version: 3.2.4`, bearer token after login.

## Endpoints

| Endpoint | Method | Status | Notes |
|---|---|---|---|
| `/login` | POST | [observed] 200 | body `{username,password,appVersion}`; returns `token.{access,refresh}` plus user profile |
| `/refresh` | POST | [comfort_HA] | body `{refresh}`; returns new `access` and `refresh`. Not yet exercised by us |
| `/accounts/me` | GET | [observed] 200 | account details |
| `/sites/` | GET | [observed] 200 | one site "Highwood"; has `schedulesEnabled`, `role: owner` |
| `/sites/{id}/zones` | GET | [observed] 200 | zones, each with embedded `adapter` summary and `holdMode` |
| `/devices/{serial}` | GET | [observed] 200 | full device state (below) |
| `/devices/{serial}/profile` | GET | [observed] 200 | list with one capability object (below) |
| `/devices/{serial}/status` | GET | [observed] 200 | firmware, wifi, setpoint limits |
| `/devices/{serial}/sensor` | GET | [observed] 404 | no wireless sensor on either unit |
| `/zones/{id}/notification-preferences` | GET | [observed] 200 | alert settings, filter reminder |
| `/sites/{id}`, `/zones/{id}` | GET | [observed] 200 | same fields as the list endpoints; nothing new |
| `/v4/sites/{siteId}/schedule-seasons` | GET | [observed] 200 | **schedules moved to v4** (the v3 route's 426 `invalidScheduleVersion` means "use v4"). Found by a subagent via github.com/ventz/mitsubishi-comfort-api-kumo `KUMO-API-V4-DOC.md` (from an iOS app 3.5.0 capture). Seasons: `{id,name,isRunning,isDefault,hasSchedules,...}`; we have Summer (not running) and Winter (running, default). Same bearer token and `x-app-version` as v3 |
| `/v4/schedule-seasons/{seasonId}/schedules` | GET | [observed] 200 | one schedule per zone: `events[{id,days,startTime,operationMode,fanSpeed,airDirection,spCool,spHeat}]`. `days` are `Su Mo Tu We Th Fr Sa`; `startTime` is 24 h `HHMM`, site local time (matches: Office's hold `endTime` 06:00Z is the 2200 event, 22:00 PST); events unsorted; the running season is the one that applies |
| `POST /v4/schedule-seasons/{id}/schedules`, `POST .../status`, `DELETE /v4/schedules/{id}/events` | | [ventz doc] **untested** | write side: create/replace schedules, start/stop a season, delete events |
| `/zones/{id}/schedules` | GET | [observed] **426** `{"error":"invalidScheduleVersion"}` | the schedule endpoint exists; it wants a schedule version we haven't found. Not a query `version`/`scheduleVersion` (1-3), not a header `x-schedule-version`/`schedule-version`/`x-app-schedule-version` (1-3), not a newer `x-app-version` (3.3.0 to 5.0.0). Next step is a capture of the Comfort app |
| `/sites/{id}/schedules`, `/sites/{id}/schedule`, `/sites/{id}/events`, `/zones/{id}/schedule`, `/zones/{id}/events`, `/zones/{id}/hold`, `/zones/{id}/programs`, `/devices/{serial}/schedule`, `/devices/{serial}/schedules`, `/devices/{serial}/events`, `/schedules` | GET | [observed] 404 | do not exist |
| `/devices/send-command` | POST | [comfort_HA] **untested by us** | `{deviceSerial, commands:{...}}` |

## Office (ductless MSZ-FX12NL) — device state fields

| Field | Example | Writable? | Notes |
|---|---|---|---|
| `power` | `1` | [comfort_HA] yes? | 0/1. comfort_HA treats power 0 as off regardless of mode |
| `operationMode` | `heat` | [comfort_HA] yes | `off cool heat dry vent auto autoCool autoHeat` |
| `previousOperationMode` | `heat` | read-only? | |
| `spCool` / `spHeat` | `26` / `19` | [comfort_HA] yes | °C, 0.5 steps. `spAuto` is null |
| `realValues.spHeat` | `20` | read-only? | differs from `spHeat` (19 vs 20): unit's actual setpoint? Investigate |
| `roomTemp` | `20.5` | read-only | °C |
| `humidity` | `null` | read-only | this unit reports none |
| `fanSpeed` | `auto` | **[observed] yes** | Office, all five names confirmed 2026-11-07 (set in the Comfort app, read back by our poll; `superPowerful` also sent by comfortd): `superQuiet` = app "Quiet", `quiet` = "Low", `low` = "Medium", `powerful` = "High", `superPowerful` = "Powerful", plus `auto`. House (3 speeds) mapping not known |
| `airDirection` | `auto` | **[observed] yes** | Office, all seven values sent by comfortd and confirmed by the next poll in 2-6 s (2026-11-07): `auto`, `swing`, `vertical` (app "Lowest"), `midvertical` ("Low"), `midpoint` ("Middle"), `midhorizontal` ("High"), `horizontal` ("Highest"). Multi-field `fanSpeed`+`airDirection` in one command also accepted |
| `connected` | `true` | read-only | |
| `rssi` | `-42` | read-only | adapter wifi |
| `ledDisabled` | `false` | [untested] | possibly writable (adapter LED) |
| `scheduleOwner` | `adapter` | [untested] | who runs the schedule |
| `scheduleHoldEndTime` | `0` | [untested] | |
| `displayConfig` | `{defrost,filter,hotAdjust,standby}` | read-only | **`filter: true` = unit wants its filter cleaned** (Hugh, 2026-11-07, app agrees). The others are assumed to be true while defrosting / standby / hot-start adjusting; not yet observed. The UI shows a yellow flag for each true one |
| `statusDisplay`, `twoFiguresCode`, `unusualFigures`, `runTest`, `tempSource`, `activeThermistor`, `collectMethod` | various | [untested] | diagnostic; meaning unknown |
| `updatedAt` | ISO time | read-only | used by comfort_HA to reconcile commands |
| `model.*` | family MSZ, 12 kBTU | read-only | static model data |

## Office — capability profile (`/profile`)

- Modes: heat yes, dry yes, vent yes, test no (`hasModeTest: false`).
- Fan: 5 speeds plus auto (`numberOfFanSpeeds: 5`, `hasFanSpeedAuto: true`).
- Vane: direction and swing both supported.
- Set point limits (°C): cool 16–31, auto 16–31, heat 10–31. `/status` says `minSetPoint 16`, `maxSetPoint 31`.
- Other flags: `extendedTemps`, `hasDefrost`, `hasHotAdjust`, `hasStandby` true; `usesSetPointInDryMode` true.

## Office — other

- Zone "Office": `hasActiveSchedule: true`, `holdMode` object (`enabled`, `holdType: until_next_event`, `endTime`, optional overrides for mode, setpoints, fan, vane). This is how the app's temporary override works. [untested] how to set it.
- Notification preferences: filter reminder every 90 days (`filterDirtyReminderInterval`), high/low temp alerts, system/zone error alerts. Filter-dirty state itself not found in dumps yet.
- Adapter firmware 03.07.02 (from `/status`).

## Differences from House (ducted PVA-AA36NL), for later

3 fan speeds; no vane control; setpoints cool 19–30, heat 10–28, auto 19–28; reports `humidity`; `hasInitialSettings` and `hasModeTest` true.

## Open questions

1. `send-command` — **observed 2026-11-07 on Office:** `{"deviceSerial":..., "commands":{"spHeat":21}}` returns `200 {"devices":["<serial>"]}` (no state in the response). `GET /devices/{serial}` showed the new value on the first poll, 5 s later, and the app agreed. One sample only: no evidence yet of comfort_HA's "up to a minute" lag. Still open: which other commands/values are accepted, how invalid values are rejected, and whether the 5 s state is the unit's confirmed state or an optimistic cloud copy.
   - `updatedAt` also changed at t+5s, t+10s and t+57s with no further commands, so it moves on every device report, not only on command confirmation. comfort_HA's `updatedAt`-based command culling needs re-checking against this.
   - The setpoint also changed on its own between our two dumps (`spHeat` 19, then 20 about hours later, before our write). Possibly the schedule or someone using the app. Worth watching.
2. Is `power` separately writable, or is `operationMode: off` the way to turn off? **Partly observed:** `{"operationMode":"vent"}` on Office returned `200 {"devices":[...]}`, state showed `vent` within 5 s with `power` still 1, and the app showed "fan only". `spHeat` (21) and `spCool` (26) were untouched. Then `{"operationMode":"off"}` set `operationMode: off` and `power: 0` together (app showed off), so off is a mode value. Still untested: `cool`, `dry`, `auto*`, `power` on its own, and what turning back on needs (`power: 1`, or a mode, which mode comes back, and whether the old setpoints/fan survive).
   - **`roomTemp` depends on mode:** 20.5 in heat, 22.5 five seconds after switching to vent, 23 after about 90 s; the app showed the same 22.5. Hypothesis (untested): the unit's sensor reads differently when the fan is circulating room air. HomeKit's Current Temperature will step when mode changes.
3. Active schedule: will the cloud overwrite a Home-initiated change at the next schedule event? Can a hold be set via the API?
4. `realValues` vs top-level fields — **hypothesis (2 sightings):** top-level `spHeat` is the value *requested* via the cloud; `realValues.spHeat` is the value the *unit itself* last reported, present only while the two differ, `{}` when in sync. Evidence: (a) first dump: `spHeat` 19, `realValues.spHeat` 20, app showed 20; (b) heat+19 command: at +4.4 s `spHeat` 19 with `realValues.spHeat` 21 (the old value), cleared by +8.6 s. If right, the app shows the unit's value when present, and a `realValues` entry that lingers means a command the unit hasn't applied. Needs a case where the unit is slow, offline, or rejects a value. Design implication: Current/Target should prefer `realValues` when present.
5. `auto` — **likely a band using both `spHeat` (floor) and `spCool` (ceiling)**: both always present, `spAuto` null on both units, comfort_HA maps auto to dual setpoints, profile has per-unit auto min/max (Office 16–31, House 19–28). Untested on the unit. **comfort_HA code read (2026-11-07):** auto reads `spHeat` as low and `spCool` as high; range writes send both; `autoCool`/`autoHeat` are only in the read-side mode map and never sent (only plain `auto` is written), so they are unit-reported sub-states. A single target in auto is written as `spCool=t, spHeat=t-1.0` ("hysteresis", maybe a real gap rule, maybe just their choice). **Our profiles have no `hasModeCool`/`hasModeAuto` keys**, only `cool`/`auto` inside `maximumSetPoints`/`minimumSetPoints`, so detect cool/auto support from those keys. Not yet read: comfort_HA's `hvac_action` (idle vs heating) logic. Unknown: is there a minimum gap between `spHeat` and `spCool`; what happens on a narrow or inverted band (reject, clamp, accept); what `autoCool`/`autoHeat` mean (sub-states the unit reports, or modes we can send).
6. Do any endpoints beyond those listed exist (schedules, history, energy)? Needs Comfort app traffic capture or probing.
7. Token lifetime and refresh behaviour in practice; rate limits.

## Decode table (Office)

Moved to `internal/annotate/annotations.toml`.

## Probe log (Office)

All times 2026-11-07. Raw copies in the gitignored `spike/out/probe_log.jsonl` (written by `spike/probe_write.py`).

| Command | HTTP / response | Observed effect | Latency |
|---|---|---|---|
| `{"spHeat":21}` | 200 `{"devices":[serial]}` | `spHeat` 20 -> 21, app agreed | <= 5 s |
| `{"operationMode":"vent"}` | 200 `{"devices":[serial]}` | mode heat -> vent, power stayed 1, setpoints unchanged, `roomTemp` 20.5 -> 22.5 (-> 23 by ~90 s), app agreed | <= 5 s |
| `{"operationMode":"off"}` | 200 `{"devices":[serial]}` | `operationMode` vent -> off and `power` 1 -> 0 in one step, setpoints unchanged, `roomTemp` stayed 23, app showed off | <= 5 s |
| `{"operationMode":"heat","spHeat":19}` (from off) | 200 `{"devices":[serial]}` | multi-field accepted; mode off -> heat, `power` 0 -> 1 automatically, `spHeat` 21 -> 19; `realValues.spHeat` showed old 21 at +4.4 s then cleared; `roomTemp` 23 -> 21 by +8.6 s; app "happy" | <= 5 s |
| `{"spHeat":19.5}` via comfortd (UI "+", from 19) | 202, command sent after the 1 s window | confirmed by the next device poll about 3 s later (`spHeat` 19 -> 19.5; `realValues.spHeat` showed the old 19 for one poll, then cleared); zone view followed about 1 min later; app showed 19.5 | ~3 s |

## Candidate HomeKit mapping (Office, MVP first)

| Feature | Maps to | Priority |
|---|---|---|
| power + `operationMode` | HeaterCooler Active + Target State (Cool/Heat/Auto) | MVP |
| `spCool` / `spHeat` | Cooling / Heating Threshold Temperature | MVP |
| `roomTemp` | Current Temperature | MVP |
| derived running state | Current State (Idle/Heating/Cooling) | MVP, needs a rule |
| `fanSpeed` | Rotation Speed | later |
| `airDirection` swing | Swing Mode | later |
| dry / fan-only modes | not in HeaterCooler; maybe separate switches | later / maybe never |
| `connected` | accessory "No Response" | MVP |

## Probe log: dry and cool (Office, 2026-11-07, Hugh asked for both)

| Sent (through comfortd's command endpoint) | Result | Time |
|---|---|---|
| `{"operationMode":"dry"}` from heat | accepted; `operationMode` dry, `power` stays 1, `previousOperationMode` dry; `spHeat` 20, `spCool` 26, fan and vane untouched | confirmed in ~6 s |
| `{"operationMode":"cool","spCool":21}` from dry | accepted as one command; `operationMode` cool, `spCool` 26 -> 21; `spHeat` untouched; `realValues.spCool` showed the old 26 for one poll, then cleared (same pattern as `spHeat`) | confirmed in ~6 s |

So `dry` and `cool` are plain mode values, and `spCool` is written like `spHeat` (0.5 steps, profile range 16-31). Still untested: `auto` (dual setpoints), `spCool` in other modes, and what the unit does at a setpoint near the room temperature.

## Probe log: auto (Office, 2026-11-07, set in the Comfort app by Hugh, read back by our poll)

| Done | Observed |
|---|---|
| Hugh chose Auto in the app (from cool 21 / heat 20) | `operationMode` `autoCool` (not plain `auto`; `previousOperationMode` followed it), `spCool` 21 -> 22, `spHeat` 20 -> 19.5 in the same poll. So the app's Auto shows as `autoCool` here, with a 2.5 degree gap, `spAuto` still null. Expect `autoHeat` when it is heating |
| comfortd sent `{"spCool":24.5,"spHeat":18}` while in `autoCool` | accepted, confirmed in ~6 s, mode stayed `autoCool`. A 6.5 degree gap is fine |
| Hugh set 19 / 25 in the app | `spCool` 24.5 -> 25, `spHeat` 18 -> 19 in one poll. A 6 degree gap |

Confirms the comfort_HA reading: auto is a band, `spHeat` the floor and `spCool` the ceiling, both written directly with the mode unchanged. Still unknown: the minimum gap, what happens on an inverted band, and whether we can send `operationMode: auto` ourselves (the app evidently can; we have not).

**Auto minimum gap (observed 2026-11-07, Hugh moving setpoints in the app):** the unit keeps `spCool - spHeat >= 1.5` and moves the setpoint he did not touch. cool 25 -> 20 pulled heat 19 -> 18.5 (gap 1.5); cool -> 18 pulled heat -> 16.5 (gap 1.5); heat 16.5 -> 17 pushed cool 18 -> 18.5 (gap 1.5). Each pair arrived in a single poll, so it is enforced in the unit or cloud, not the app. Larger gaps (2.5, 6, 6.5) are untouched. Not yet seen: a gap between 1.0 and 1.5, or what the unit does when we send a pair below 1.5. comfort_HA's 1.0 offset is therefore too small.

**Sending `operationMode: auto` ourselves (Office, 2026-11-07, from the web UI at Hugh's press):** accepted. From heat (spHeat 20, spCool 18.5) the unit reported plain `auto` 3 s later, then `autoCool` at +15 s. On entering auto it kept its stored `spCool` (18.5) and pulled `spHeat` 20 -> 17 to hold the 1.5 gap, so **switching into auto overwrites the heat setpoint**. comfortd's hold confirms on `autoCool`/`autoHeat` as well as `auto`; no `unconfirmed` record.

**Dry mode setpoint (Office, 2026-11-07, Hugh in the app):** switching autoCool -> dry left `spCool` 18.5 and `spHeat` 17 as they were, and the app showed 18.5 as the dry target. Hugh then set it to 20: `spCool` 18.5 -> 20, `spHeat` unchanged. **Dry uses `spCool`.** (Matches `usesSetPointInDryMode: true`.)
