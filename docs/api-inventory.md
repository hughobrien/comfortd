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
| `/devices/{serial}/mhk2` | GET | [observed] 200 (House only) | the MHK2 wall thermostat: `model` "MHK2", `thermostat` true, `thermostatBattery`, `humidity` (matches the device's), `scheduleOwner`/`scheduleEnabled`, `holdAdapterEndTime` (epoch seconds, equals the zone hold's `endTime`), `holdAdapterCancelMhk2`, `holdMhk2CancelAdapter`, `holdMhk2EndTime`, `autoOwner`/`autoStatus`. Documented in ventz's v4 doc. House's `roomTemp` and humidity very likely come from it. The `sensor` endpoint is for the separate wireless sensor and 404s. Polled when the zone says `adapter.hasMhk2`. Live House body also has `id`, `serial`, `firmware`, `autoSpHeat/Cool`, `outdoorAir*` and `scheduleEnabled` as "enabled"/"disabled" text. Other projects: ventz's doc also lists Socket.IO `force_adapter_request` kind `mhk2` and `PATCH /v3/zones/{zone}/notification-preferences` `{"mhk2LowBattery":true}`; no project documents an MHK2 write or hold-set |
| `/v4/sites/{siteId}/schedule-seasons` | GET | [observed] 200 | **schedules moved to v4** (the v3 route's 426 `invalidScheduleVersion` means "use v4"). Found by a subagent via github.com/ventz/mitsubishi-comfort-api-kumo `KUMO-API-V4-DOC.md` (from an iOS app 3.5.0 capture). Seasons: `{id,name,isRunning,isDefault,hasSchedules,...}`; we have Summer (not running) and Winter (running, default). Same bearer token and `x-app-version` as v3 |
| `/v4/schedule-seasons/{seasonId}/schedules` | GET | [observed] 200 | one schedule per zone: `events[{id,days,startTime,operationMode,fanSpeed,airDirection,spCool,spHeat}]`. `days` are `Su Mo Tu We Th Fr Sa`; `startTime` is 24 h `HHMM`, site local time (matches: Office's hold `endTime` 06:00Z is the 2200 event, 22:00 PST); events unsorted; the running season is the one that applies |
| `POST /v4/schedule-seasons/{id}/schedules` | POST | [observed] 201 | body `{seasonId, schedules:[{zone, events:[{startTime,days,operationMode,fanSpeed,airDirection,spCool,spHeat}]}]}`. **Adds** the events to the zone's schedule (existing events are kept, not replaced); response echoes the whole schedule, new event with a fresh id. Tested on Office 2026-11-07 |
| `DELETE /v4/schedules/{scheduleId}/events` | DELETE | [observed] 204 | body `{events:[eventId]}`; `scheduleId` is the per-zone schedule id. Tested on Office |
| `POST .../status` | | [ventz doc] **untested** | start/stop a season |
| `POST /v4/sites/{siteId}/hold` | POST | [observed] **201** (Office, 2026-11-17, Hugh agreed) | The Comfort app's `updateHoldMode` (ServiceUrls `SITE_HOLD`, `apiVersion: '4'`; body is the payload object as built). Payload shapes read from the app's handlers: **end hold** `{siteId, zones:[{id:<zoneId>, enabled:false, type:"hold"}]}`; **extend** `{siteId, endTime:<chosen time>, zones:[{id, enabled:true, type:"hold"}]}`; **hold permanently** same with `endTime: null`. Enums: `type` away / hold / unset; `holdType` until_next_event / until_specific_time / permanent / unset (`holdType` is not sent by these handlers, so the cloud derives it from `endTime`; inference). The app has no call that creates a hold on a setting change: its "hold created" toast fires when the zone's `holdMode.enabled` turns true, so the cloud (or adapter, `origin: adapter`) creates it. **Tested (extend shape, zone with no hold):** `endTime` must be a number: a string gave 400 `{"error":{"endTime":"Expected number, received string"}}`; epoch **seconds** (1794896520) gave 201 `{"endTime":<n>,"holdModes":[{id,enabled:true,type:"hold",holdType:"until_specific_time",origin:"adapter",endTime:"2026-11-17T06:22:00.000Z",...}]}`. The zone view showed the hold within 5 s (no poll lag). So comfortd can create a hold, and `holdType` follows `endTime` (specific time here). **End (tested the same day):** `{siteId, zones:[{id, enabled:false, type:"hold"}]}` (no `endTime`) gave 201 `{holdModes:[{enabled:false, holdType:"permanent", endTime:<now>}]}`, the same end state as the app's 'End hold'. **Permanent (tested):** `endTime: null` gave 201 with `holdType: permanent` and an `endTime` stored a hundred years ahead (2126). **Extending an existing hold (tested):** the same body with a number turns a permanent hold into `until_specific_time` at that time. **Schedule interaction (tested 2026-11-17, Office):** a hold made this way suppresses a schedule event that falls inside it (a 12:48 event did nothing), and when the hold ends the schedule's current event, the latest one that has passed, is applied within seconds (12:58:04), so the unit does not return to its earlier state. Not yet tried: whether it survives a fan command the way an app hold does. comfortd's web UI uses this endpoint for its hold control (`pkg/kumo` `SetHold`/`EndHold`). Source of the shapes: iOS app 3.6.0 `main.jsbundle` (Hermes v96) via hermes-dec; see issue 11 |
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
| `fanSpeed` | `auto` | **[observed] yes** | Office, all five names confirmed 2026-11-07 (set in the Comfort app, read back by our poll; `superPowerful` also sent by comfortd): `superQuiet` = app "Quiet", `quiet` = "Low", `low` = "Medium", `powerful` = "High", `superPowerful` = "Powerful", plus `auto`. House (3 speeds) confirmed the same way 2026-11-08: `quiet` = app "Low", `low` = "Medium", `powerful` = "High", plus `auto` (the app also changed the hold on each change) |
| `airDirection` | `auto` | **[observed] yes** | Office, all seven values sent by comfortd and confirmed by the next poll in 2-6 s (2026-11-07): `auto`, `swing`, `vertical` (app "Lowest"), `midvertical` ("Low"), `midpoint` ("Middle"), `midhorizontal` ("High"), `horizontal` ("Highest"). Multi-field `fanSpeed`+`airDirection` in one command also accepted |
| `connected` | `true` | read-only | |
| `rssi` | `-42` | read-only | adapter wifi |
| `ledDisabled` | `false` | [untested] | possibly writable (adapter LED) |
| `scheduleOwner` | `adapter` | [untested] | who runs the schedule |
| `scheduleHoldEndTime` | `0` | [untested] | |
| `displayConfig` | `{defrost,filter,hotAdjust,standby}` | read-only | **`filter: true` = unit wants its filter cleaned** (Hugh, 2026-11-07, app agrees). The others are assumed to be true while defrosting / standby / hot-start adjusting. **`hotAdjust` observed** (2026-11-16/17): on Office in heat it is true, with `statusDisplay` 4, for 2 to 6 minutes about every 30 to 60 minutes while the room hovers around the setpoint; House shows it with `statusDisplay` 5. It sometimes starts within seconds of a fan change (about 3 of 23 fan changes), not usually. Meaning not documented anywhere public; the MSZ-FH service manual's "cold air prevention" (indoor fan stops or runs Very Low while the coil is cold, whatever the fan setting) is the best fit. `statusDisplay` values have no public meaning. The UI shows a yellow flag for each true one |
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

- Zone "Office": `hasActiveSchedule: true`, `holdMode` object (`enabled`, `holdType: until_next_event`, `endTime`, optional overrides for mode, setpoints, fan, vane). This is how the app's temporary override works. [untested] how to set it over the API; no public project documents a hold write.
- **Hold behaviour [observed] (history, 2026-11-08 and 2026-11-17):**
  - A hold opens after ANY change made in the Comfort app while none is active, even a change back to the schedule's own value (Office fan `superPowerful` to `auto`, where every Office event has fan `auto`). It appeared in the zone view within one poll.
  - A hold has never opened after a comfortd `send-command` (about 25 commands, both body shapes). Commands work with or without a hold.
  - `endTime` is the next schedule event; the hold lapses about 14 s after it and the event applies in the same poll.
  - "End hold" in the app (2026-11-17 02:24:54Z): `enabled` true to false, `endTime` set to the moment of the press, `holdType` changed to `permanent`, and the unit reverted to the schedule's current event (fan `low` to `auto`). The next app change put `holdType` back to `until_next_event`.
  - House once got a hold (2026-11-08 06:39Z) with no recorded field change; cause unknown.
  - House's zone carries it as `holdMode.*` and `scheduleHoldEndTime`; the mhk2 `holdAdapterEndTime` lags it by hours (slow tick).
- Notification preferences: filter reminder every 90 days (`filterDirtyReminderInterval`), high/low temp alerts, system/zone error alerts. Filter-dirty state itself not found in dumps yet.
- Adapter firmware 03.07.02 (from `/status`).

## Differences from House (ducted PVA-AA36NL), for later

3 fan speeds; no vane control; setpoints cool 19–30, heat 10–28, auto 19–28; reports `humidity`; `hasInitialSettings` and `hasModeTest` true.

## Open questions

1. `send-command` — **the cloud echo is not delivery or confirmation.** ventz's v3 doc: commands to an unreachable unit return 200 and reach nothing; the library behind the official HA integration says acceptance does not confirm device state. Our "confirmed" means only that the next poll showed the value (the device record shows it as soon as 0.2 to 3 s). Delivery behind the echo is asynchronous: on 2026-11-08 06:08Z a comfortd `auto` was overwritten about 5 s later by a still-queued `quiet`. Until 2026-11-17 no comfortd command had a recorded physical effect; since then `fanSpeed low` and `superPowerful` from the web UI were heard to work (with and without a hold). One failure: `superPowerful` at 01:32Z in heat mode did nothing audible while `hotAdjust` was on. **Resolved 2026-11-17 03:03Z:** with Office switched to `vent` (fan only) and `fanSpeed` still `superPowerful`, Hugh heard it 'very loud'; the same value from the same path was silent while the unit idled or warmed up in heat. So cloud fan commands do reach the unit, with or without a hold; in heat mode the unit may hold the indoor fan low whatever the setting (cold air prevention). **Observed 2026-11-07 on Office:** `{"deviceSerial":..., "commands":{"spHeat":21}}` returns `200 {"devices":["<serial>"]}` (no state in the response). `GET /devices/{serial}` showed the new value on the first poll, 5 s later, and the app agreed. One sample only: no evidence yet of comfort_HA's "up to a minute" lag. Still open: which other commands/values are accepted, how invalid values are rejected, and whether the 5 s state is the unit's confirmed state or an optimistic cloud copy.
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

## The Comfort app's whole REST surface (iOS 3.6.0)

Read from the app's `ServiceUrls` table and its `ApiServices` functions in the hermes-dec decompile (issue 11); path, method and version only. Version 3 is the default; `(v4)` marks calls that set `apiVersion: '4'` explicitly (the schedule and hold families). **[app code, not called by us]**: methods are as the app issues them; request bodies are built by each caller and are not decoded here, except where the table above says otherwise. Service names are the app's function names.

- **accounts**: POST /accounts `signup`; PATCH /accounts/delete `deleteAccount`; POST /accounts/fcm `sendPushNotificationsToken`; POST /accounts/fcm/delete `deletePushNotificationsToken`; POST /accounts/forgot-password `forgotPassword`; GET /accounts/masquerade/pending `getPendingMasqueradeRequests`; POST /accounts/masquerade/{id}/approve `approveMasqueradeRequest`; POST /accounts/masquerade/{id}/deny `denyMasqueradeRequest`; GET /accounts/me `me`; PATCH /accounts/me `updateMe`; POST /accounts/pin-info `getContractorPinInfo`; PUT /accounts/preferences `updateUserPreferences`; POST /accounts/reset-password `resetPassword`; PATCH /accounts/revert-hijack `revertHijack`; POST /accounts/send-verification-email `sendEmailVerification`; POST /accounts/update-password `changePassword`; POST /accounts/update-username `updateUsername`; POST /accounts/validate-reset-code `validateResetCode`; POST /accounts/verify-email `verifyEmail`
- **address-autocomplete**: POST /address-autocomplete `getAddressAutocomplete`
- **comfort-settings**: POST /comfort-settings `createComfortSetting`; GET /comfort-settings/presets `getPresetComfortSettings`
- **config**: GET /config/new-version-overlay `getVersionOverlayConfig`
- **contractors**: GET /contractors `getContractors`
- **demo**: POST /demo/claim (v4) `claimDemoSite`; POST /demo/unclaim (v4) `unclaimDemoSite`
- **devices**: POST /devices/config-key `getAdapterConfigKey`; POST /devices/send-command `sendCommand`; POST /devices/unregister `unregisterAdapter`; GET /devices/{serial} `getAdapter`; PATCH /devices/{serial} `updateAdapter`; GET /devices/{serial}/acoil-settings `getAdapterAcoilSetting`; GET /devices/{serial}/auto-dry `getAutoDry`; GET /devices/{serial}/ble-reset `getAdapterBleResetCode`; GET /devices/{serial}/initial-settings `getAdapterInitialSettings`; GET /devices/{serial}/kumo-properties `getKumoStationProperties`; GET /devices/{serial}/mhk2 `getMhk2`; GET /devices/{serial}/profile `getDeviceIndoorUnitProfile`; GET /devices/{serial}/prohibits `getProhibits`; GET /devices/{serial}/recent-connected `checkAdapterRecentlyConnected`; GET /devices/{serial}/sensor `getAdapterSensor`; GET /devices/{serial}/status `getAdapterStatus`; POST /devices/{serial}/relay-command `relayCommand`
- **dr-programs**: GET /dr-programs/{id}/events `getProgramEvents`; POST /dr-programs/{id}/optOut `optOutProgramEvent`
- **exchange-token**: POST /exchange-token `exchangeToken`
- **groups**: POST /groups `createGroup`; DELETE /groups/{groupId}/zone/{zoneId} `ungroupZone`; GET /groups/{id} `getGroup`; PATCH /groups/{id} `updateGroup`; POST /groups/{id}/add-zones `addZonesToGroup`; POST /groups/{id}/changeover `updateGroupChangeoverSettings`; DELETE /groups/{id}/ungroup `ungroupAllZones`
- **kumo-station**: POST /kumo-station `addKumoStation`; POST /kumo-station/{id}/accessory `addKumoStationAccessory`; DELETE /kumo-station/{id}/accessory/{channelNumber} `removeKumoStationAccessory`; PATCH /kumo-station/{id}/accessory/{channelNumber} `updateKumoStationAccessory`
- **login**: POST /login `login`
- **logout**: POST /logout `logout`
- **materials**: GET /materials/gallery `getUnitMaterialGalleries`; GET /materials/models `getUnitMaterials`
- **migrate-schedule**: POST /migrate-schedule (v4) `migrateScheduleAccount`
- **notifications**: GET /notifications/active `getActiveNotifications`; GET /notifications/active/unseen-count `getUnseenActiveNotificationsCount`; GET /notifications/resolved `getResolvedNotifications`; GET /notifications/resolved/unseen-count `getUnseenResolvedNotificationsCount`; PATCH /notifications/seen `markNotificationsAsSeen`
- **schedule-seasons**: DELETE /schedule-seasons/{id} (v4) `deleteScheduleSeason`; PATCH /schedule-seasons/{id} (v4) `updateScheduleSeasonName`; DELETE /schedule-seasons/{id}/clean (v4) `clearScheduleEvents`; GET /schedule-seasons/{id}/schedules (v4) `getSchedules`; POST /schedule-seasons/{id}/schedules (v4) `updateSchedules`; POST /schedule-seasons/{id}/set-default (v4) `setDefaultScheduleSeason`; POST /schedule-seasons/{id}/status (v4) `updateScheduleSeasonStatus`
- **schedule**: PATCH /schedule/{id} `updateSchedule`; PATCH /schedule/{id}/duplicate `duplicateSchedule`
- **schedules**: DELETE /schedules/{id}/events (v4) `deleteEventsFromSchedule`
- **sensors**: POST /sensors `addSensor`; DELETE /sensors/{id} `removeSensor`
- **sites**: DELETE /site/{id}/preferable-contractor `removePreferableContractor`; GET /site/{id}/preferable-contractor `getPreferableContractor`; PUT /site/{id}/preferable-contractor `updatePreferableContractor`; POST /site/{id}/request-service `requestContractorService`; GET /sites `getSites`; POST /sites `createSite`; GET /sites/transfers/pending `getPendingSiteTransfers`; GET /sites/transfers/{id}/cancel `cancelSiteTransfer`; GET /sites/transfers/{id}/confirm `confirmSiteTransfer`; GET /sites/transfers/{id}/resend `resendSiteTransfer`; DELETE /sites/{id} `deleteSite`; GET /sites/{id} `getSiteDetails`; PATCH /sites/{id} `updateSite`; GET /sites/{id}/dr-programs `getPrograms`; GET /sites/{id}/groups `getGroups`; POST /sites/{id}/hold (v4) `updateHoldMode`; GET /sites/{id}/kumo-station `getKumoStation`; DELETE /sites/{id}/program-enroll `unenrollFromProgram`; GET /sites/{id}/program-enroll `getEnrolledPrograms`; POST /sites/{id}/program-enroll `enrollInProgram`; POST /sites/{id}/program-enroll/resubmit `resubmitProgram`; PATCH /sites/{id}/program-enroll/zones `updateEnrolledZones`; GET /sites/{id}/schedule-seasons (v4) `getScheduleSeasons`; POST /sites/{id}/schedule-seasons (v4) `createScheduleSeason`; PATCH /sites/{id}/timezone `updateSiteTimeZone`; PATCH /sites/{id}/toggle-notifications `updateSiteNotificationPreferences`; PATCH /sites/{id}/toggle-schedules `toggleSiteSchedules`; GET /sites/{id}/weather `getWeather`; GET /sites/{id}/zones `getZones`; POST /sites/{id}/transfer-ownership `initiateSiteTransfer`
- **zones**: POST /zones `createZone`; GET /zones/{id} `getZone`; PATCH /zones/{id} `updateZone`; GET /zones/{id}/comfort-settings `getZoneComfortSettings`; GET /zones/{id}/connection-history `getZoneConnectionHistory`; GET /zones/{id}/notification-preferences `getZoneNotificationPreferences`; PATCH /zones/{id}/notification-preferences `updateZoneNotificationPreferences`; PATCH /zones/{id}/reset-filter `resetZoneFilterNotificationPreferences`; GET /zones/{id}/schedules `getZoneSchedules`; GET /zones/{id}/switch-schedule `switchActiveSchedule`

## Request headers and Socket.IO (iOS 3.6.0)

From the decompile (issue 11); **[app code, not called by us]**.

- **Headers.** The app's HTTP client sets `Authorization: Bearer <access token>` after login and clears it on logout; its `HeaderKeys` table also names `X-APP-VERSION`, `APP-ENV` and `X-ALLOW-CACHE` (the last set to `true` or `false` per request group). The base URL has any trailing `/v<N>` stripped; the version is chosen per call.
- **Socket.IO.** The app opens a Socket.IO client to `https://socket-prod.kumocloud.com/` (path `/socket.io`; `socket-stg` and `socket-dev` for the other environments), authenticating with `extraHeaders: {Authorization: 'Bearer <access token>'}` and reconnecting with the same token.
- **Subscribing.** The client emits `subscribe` with a device serial (a plain string) and expects `subscribed`; `unsubscribe` and `unsubscribed` mirror it. It also subscribes to the user (`subscribeToUser`), remembers the serials and re-subscribes after a reconnect. For serials it cannot subscribe to it falls back to polling timers.
- **Events the app listens for:** `device_update`, `device_status_v2`, `hold_update`, `adapter_update`, `profile_update`, `prohibits_update`, `autodry_update`, `acoil_update`, `eqc_update`, `eqc_properties_update`, `sensor_update`, `system_change_over_update`, `schedule_version_migrated`, `notification_channel`, `app_update_channel`, plus the connection events `connect`, `reconnect`, `reconnect_error`, `disconnect`, `error`. `hold_update` is the push for hold changes.
- **Emitted besides subscribe:** `force_adapter_request`, sent as two positional arguments, `(serial, type)`, with no object body. `type` is one of `iuStatus`, `sensor`, `mhk2`, `equipment-full`, `eqc-channel-1` to `-4`, `profile`, `adapterStatus`, `autodry`, `systemChangeOver`, `time`, `acoil`, `prohibits`. The app allows one per serial-and-type a minute (a per-key timestamp check), and a caller can override it. My earlier note that the app emits `device_status_v2` was wrong: it only listens for it.
- **Event payloads (from the handlers):**
  - `hold_update`: an object with `deviceSerial` plus the hold fields. The app builds a `HoldMode` from it: `enabled`, `type` (`hold`, `away` or `unset`), `holdType` (`until_next_event`, `until_specific_time`, `permanent` or `unset`), `endTime`, and the held values `spCool`, `spHeat`, `operationMode`, `fanSpeed`, `airDirection`, plus a demand-response `drProgramEvent` (`enabled`, `endTime`, `programName`). It stores that as the zone's `holdMode`. The app counts a hold as "triggered by the adapter" when `type` is `hold`, `enabled` is true, `holdType` is not `unset` and `endTime` is non-empty.
  - `device_update`: the whole device record, the same fields as the `device` endpoint: `deviceSerial`, `operationMode`, `previousOperationMode`, `power`, `spCool`, `spHeat`, `spAuto`, `fanSpeed`, `airDirection`, `roomTemp`, `rssi`, `connected`, `realValues`, `updatedAt`, `twoFiguresCode`. If `displayConfig` is present it is built as an `Adapter`, otherwise as a `SocketAdapter`. It feeds the app's optimistic state.
  - `device_status_v2`: `deviceSerial`, `status` (`connected` or `disconnected`), `lastTimeConnected`, `lastTimeDisconnected`, `hasIduCommunicationError`. The app uses it to update the zone's connected flag and last-status timestamp.
- **Not decoded yet:** payloads of the other update events, and request bodies for the REST calls above (built by each caller). A subscribe-only listener could replace polling for state changes, and `hold_update` plus `device_update` would show the app's changes as they happen; that is untested and would need Hugh's agreement before connecting.

### Socket.IO observed live (2026-11-17, read-only)

A throwaway client (python-socketio, Bearer token from the v3 login, `websocket` transport, `subscribe <serial>` for both units; sent nothing else) connected and stayed up for 5 minutes while Hugh used the Comfort app on Office. **[observed]**

- **On subscribe** the server pushes a snapshot: a `device_update` (full) per unit within a second, then `profile_update` and `adapter_update` per unit a few seconds later (repeated about every 15 s while connected).
- **`device_update` has two shapes**, told apart by `payloadType`. `"full"`: the whole device record (`id`, `deviceSerial`, `rssi`, `power`, `operationMode`, `humidity`, `scheduleOwner`, `scheduleHoldEndTime`, `fanSpeed`, `airDirection`, `roomTemp`, `spCool`, `spHeat`, `spAuto`, `modelNumber`, `serialNumber`, `connected`, `previousOperationMode`, `lastStatusChangeAt`, `updatedAt`, `displayConfig`, `timeZone`, `realValues`, `date`). `"fast"`: just the live fields (`rssi`, `roomTemp`, `operationMode`, `power`, `spCool`, `spHeat`, `airDirection`, `fanSpeed`, `connected`, `displayConfigFast`, `date`). A setpoint change made in the app arrived as a fast update (20 to 20.5, and back) and then in a full one.
- **`hold_update`** arrives for each hold change, **twice** (about 10 ms apart): `{deviceSerial, date, id, enabled, type:"hold", holdType, origin:"adapter", endTime, operationMode, fanSpeed, airDirection, spCool, spHeat}`. The held values were all null for an until-next-event hold. Starting a hold in the app: `enabled` true, `holdType` `until_next_event`, `endTime` the next schedule event. Ending it: `enabled` false, `holdType` `permanent`, `endTime` the moment of ending; the first of the pair carried an `endTime` a minute earlier than the second (same minute-rounding the REST zone shows). `origin` is `adapter` either way, so it does not say who started the hold.
- **Order of events:** the app's setpoint change back to the schedule's value (20.5 to 20) opened a new hold at 22:17:08, and the `hold_update` for it arrived about 14 s **before** the `device_update` that carried the new setpoint (22:17:22). So `hold_update` is the faster signal, the `fast` device updates come on roughly a 10 to 15 s beat, and Hugh had to end the hold twice (confirms that any app change opens a hold, even back to the schedule's own value).
- **`scheduleHoldEndTime`** in the full device record stayed 0 while a hold was on, so the hold is only visible through `hold_update` (or the zone endpoint).
- **`profile_update`**: capabilities (`hasModeDry`, `hasVaneDir`, `numberOfFanSpeeds`, `minimumSetPoints`/`maximumSetPoints`). **`adapter_update`**: `firmwareVersion`, `routerSsid`, `routerRssi`, `minSetpoint`/`maxSetpoint`, `lastUpdated`.
- **Latency:** the app's changes reached the socket within about a second of the event timestamps, against our 120 s poll.
- **Not seen:** `device_status_v2`, `schedule_version_migrated` and the other update events (nothing triggered them).

- **No `subscribed` reply.** The real server answers `subscribe <serial>` with no `subscribed` event (only the snapshot `device_update` follows); comfortd's client counts replies if they come but does not depend on them. Seen 2026-11-17 on the first deploy: events flowed with the reply count at 0.

## Setpoint ranges: what the app shows (House, 2026-11-17)

Hugh read the ranges in the Comfort app on House with its setpoint-limit setting on and off, and on Office, and compared them with the profile (`minimumSetPoints`/`maximumSetPoints`) and the status endpoint (`minSetPoint`/`maxSetPoint`, also pushed as `adapter_update`). **[observed]**

| Unit, state | status min/max | app cool | app heat |
|---|---|---|---|
| House, limit on | 19 / 25 | 19 to 30 | 10 to 25 |
| House, limit off | 19.5 / 28 | 19.5 to 30 | 10 to 28 |
| Office | 16 / 31 | 16 to 31 | 10 to 31 |

House's profile is cool 19 to 30, heat 10 to 28, auto 19 to 28; Office's is cool 16 to 31, heat 10 to 31. The rule that fits every row: **cool floor = max(profile min, status min); heat ceiling = min(profile max, status max); the other ends are the profile's.** (Auto: narrowed on both ends, a conservative guess; not read in the app.) The status endpoint is the source that follows the app setting (it read 19.5/28 within seconds of the setting being turned off); the pushed `adapter_update` was seconds late and did not repeat, so comfortd refreshes status itself, on every backstop poll.

## Writing the Min/Max setpoints (iOS 3.6.0 decompile; **tested on House 2026-11-18, Hugh agreed**)

The app's "Min/Max setpoints" screen (`zoneSettings.temperatureLimitsUpdated`) sets the adapter limits that show up in the status endpoint as `minSetPoint`/`maxSetPoint` (see the table above). From the decompile: it sends `POST /v3/devices/{serial}/relay-command` (ApiServices `relayCommand`, ServiceUrls `RELAY_COMMAND`) with the payload `{serial, adapter: {status: {userMinCoolSetPoint: <°C>, userMaxHeatSetPoint: <°C>}}}`, the two values converted from the user's display unit first (`convertForBlewit`). Turning the feature on or off is also a user preference, `isMinMaxSetpointsEnabled` (default false), saved with the account preferences; what the app writes when it is turned off is not decoded (House read 19.5 / 28 afterwards, which may be the factory limits). Note `updateAdapter` is `PATCH /v3/devices/{serial}` and is a different call. Open: the response, whether the cloud echoes the new limits at once, and whether 0.5 steps are required.

**Tested (House, one call, Hugh agreed):** `POST /v3/devices/{serial}/relay-command` with `{"serial": <serial>, "adapter": {"status": {"userMinCoolSetPoint": 19, "userMaxHeatSetPoint": 24}}}` and the headers the app uses (`Authorization: Bearer`, `x-app-version: 3.6.0`) answered **200** with `{"adapter":{"status":{"userMaxHeatSetPoint":24,"userMinCoolSetPoint":19}}}`. The status endpoint read `minSetPoint 19`, `maxSetPoint 24` (was 19.5 / 28, `lastUpdated` moved to the call time) on the first read 5 s later, so the cloud reflects the new limits at once. Plain °C numbers are accepted; the response echoes what was sent.

**The on/off switch (read 2026-11-18, write not yet tested):** the app's "Min/Max setpoints" feature is gated by the account preference `isMinMaxSetpointsEnabled` (in `GET /v3/accounts/me` → `preferences`; it was `false` while House's adapter limits were already 19/24, which is why the app showed the feature as off). It is one setting for the whole account, not per unit. The app turns it on or off with `PUT /v3/accounts/preferences`, sending the preferences object with that flag changed (decompile: `Object.assign({}, preferencesToJson(user), {isMinMaxSetpointsEnabled})`). comfortd does the same: read the preferences, change the one flag, PUT the whole object back. The body shape (bare preferences object) is inferred, not observed.

## LAN credentials (issue 6; researched 2026-11-18)

Local control of a Kumo adapter needs two per-device secrets: the adapter `password` and the `cryptoSerial`. Until about 2026-07-28 the v3 cloud served them: `cryptoSerial` in `GET /devices/{serial}/status`, `password` in the Socket.IO `adapter_update` event. Since about 2026-08-01 it serves neither, for anyone; comfortd's own captures agree (status keys are `mac, routerSsid, routerRssi, minSetPoint, maxSetPoint, firmwareVersion, roomTempDisplayOffset, lastUpdated`; `adapter_update` carries the same, no `password`). **[observed here]** Also checked, by key name only: `/devices/{serial}`, `/profile`, `/sensor`, `/kumo-properties`, `/initial-settings`, zones, sites, `/accounts/me`, login.

Reported by others (not verified here): the legacy v2 login (`POST https://geo-c.kumocloud.com/login` in the US, `https://mesca-prod.kumocloud.com/login/v2` in Canada; body `username`, `password`, `appVersion` "2.2.0") still returns both secrets, but only for units registered before the Comfort switchover; newer accounts get HTTP 500. Re-adding a unit does not help. The adapter's own local API does not reveal the secrets (`cryptoSerial` appears nowhere; `password` is write-only). Local control is `PUT http://<unit-ip>/api?m=<token>` with a SHA-256 token over the body, the password and the cryptoSerial. **Tried here (2026-11-18, Hugh agreed):** (1) the legacy v2 login for this account: the US host (`geo-c`) answered HTTP 200 with a `null` body (the account has no legacy records) and the Canadian host HTTP 500, so no secrets from v2. (2) Both adapters are on the LAN (found by the MAC from `/status` in the router's neighbour table) and listen only on TCP port 80 (a port scan of 1-1024 plus a few common ports found nothing else). `GET /` is a 404, and `PUT /api?m=<invalid token>` answers `{"_api_error": "device_authentication_error"}`, so the local API is present and reachable and only the secrets are missing. (3) A 90 s capture on the router showed each adapter holding one long-lived outbound TCP 443 connection to an AWS us-west-2 address, carrying only TLS application data (record header `17 03 03`), so a passive capture cannot show the secrets. Seeing the TLS handshake would need the adapter to reconnect, and reading its contents would need breaking its TLS, which was not attempted. `POST /v3/devices/config-key` (`getAdapterConfigKey`) was not called.

**Pending and live (issue 1, built 2026-11-18):** a command's field is *pending* from the moment we send it. It is *live* once the device record shows the requested value **and** `realValues` has no differing entry for that field, i.e. the unit itself has reported it (see the `realValues` hypothesis above; seen on `spHeat` and `spCool`). A cloud echo alone no longer clears pending. A field that never goes live times out as `unconfirmed` in the history, as before. This relies on the `realValues` hypothesis; it is still only a few sightings, and `realValues` was never seen for mode, fan or vane. A fast push update leaves `realValues` as it was until the next full record or poll.

**Pending to live, measured on Office (2026-11-18, Hugh agreed): spHeat 19.5 to 20 and back.** Command sent at t=0; the cloud record showed the new value at about +4 s (our 1 s coalescing window included) with `realValues.spHeat` holding the old value; `realValues` went to `{}` about 5 s later (+9 to +10 s from the send). Identical in both directions. No `unconfirmed` row. So a cloud echo does come well before the unit reports, and `realValues` is a usable "unit has it" signal for setpoints.

## What "hot adjust" and "standby" are (researched 2026-11-18, issue 2)

- **`displayConfig.hotAdjust` = the heating warm-up phase.** Mitsubishi's City Multi PMFY manual (control modes, p.17) defines "stand by (hot adjust)" mode: it starts when heating starts, when the thermostat goes from off to on, and after a defrost ends, and it shows a STANDBY symbol only then. While it lasts the indoor fan stops or runs extra low until the coil is warm, whatever fan speed is set (the PMFY flow chart gives about 2 minutes, then up to 5 or until the liquid pipe is warm; garbled extraction, so approximate). That matches our 2 to 6 minutes. <https://babycare.manualsonline.com/manuals/mfg/mitsubishi/pmfyp08nbmue.html?p=17> **[manufacturer doc, related family]**
- **For the MSZ-FX (Office's family) the same thing is called "cold air prevention control"** (service manual OBH945B section 8-4): "When the compressor is not operating or is starting, and the temperature of indoor heat exchanger and/or the room temperature is low or when defrosting is being done, the indoor fan will stop or rotate in Very Low speed", and the horizontal vane is set upward (8-6(8)). It uses neither "hot adjust" nor "hot start". <https://gotductless.com/cdn/shop/files/MSZ-FX06NL_Service_Manual.pdf> **[manufacturer doc]**. This also covers a unit idling at the setpoint (compressor not operating). Not found for the PVA-AA36NL (R454B) service manual; the older PVA-A manual has no such text, so House's behaviour is assumed to be alike.
- **`displayConfig.standby`: not idle.** The muart CN105 protocol reference reads run state 0x08 "standby" as another unit with priority requesting a conflicting mode (a multi-system thing), and 0x04 "preheat" as hot adjust. Some Kumo projects call `standby` "compressor idle"; that is unconfirmed and disagrees with muart. <https://muart-group.github.io/developer/it-protocol/0x62-get-response/0x09-get-run-state> **[community]**
- **`twoFiguresCode`** is the unit's two-character status/fault code ("A0" = normal) **[community]**. **`statusDisplay` (4 on Office, 5 on House while hotAdjust was true) has no published meaning.** The Kumo owner manual shows a "DEFROST" label in the app; no "hot adjust" or "standby" label was found there or on the MHK2.
- The card's fan hint now says the fan "stays off or very low until the coil is warm" while `hotAdjust` is on, or "may stay off or very low" in heat when the room is at or above the setpoint. It does not use `standby`.
