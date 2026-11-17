# Hold control (short design)

Approved by Hugh in conversation, 2026-11-17. Closes issues 12 and 13 in part (no HomeKit, no House).

## Intent

Let Hugh set and end a Comfort "hold" from the web UI, so a change he makes in comfortd can last past the schedule's next event, as a change made in the Comfort app does. The cloud requests were proven on Office on 2026-11-17 (docs/api-inventory.md, `POST /v4/sites/{siteId}/hold`).

## Goals

- A radio group on the season line of the Schedule section: **No hold / Until next event / Until [time] / Permanent**.
- The group shows the current hold, including holds made in the Comfort app.
- Only writable units get it (Office; House stays read-only).

## Non-goals

HomeKit hold switch, House/MHK2 holds (issue 16), away holds (issue 17), making commands open a hold automatically (issue 14). The existing hold flag by the unit name stays as status.

## Approaches

1. **Radio group on the season line (chosen).** Shows the whole state and exposes permanent.
2. Dropdown, or a toggle plus a menu. Hides the options Hugh just found. Rejected.

## Design

- **Requests** (`POST /v4/sites/{siteId}/hold`, body `{siteId, [endTime], zones:[{id, enabled, type:"hold"}]}`):
  - Until a time: `endTime` as epoch **seconds** (a string gives 400), `enabled:true`.
  - Permanent: `endTime: null`, `enabled:true`.
  - No hold: `enabled:false`, no `endTime`. This re-applies the schedule's current event, so the UI says so.
- **Until next event** sends the next event's time (already computed for the card); hidden when the schedule has none. **Until [time]** is today at that time in the schedule's timezone, or tomorrow if past.
- **State display** comes from the zone's `holdMode.enabled` and `holdType`; a permanent hold's far-future `endTime` is never shown. Until-time holds show the time.
- **kumo client:** `SetHold` / `EndHold`, site ID from the sites list, zone ID from the zones list.
- **Control layer:** new intents behind the `writableUnits` guard (server-side, not just the template); history record per write (endpoint `hold`, source `ui`); a kick poll after the write.
- **kumotest fake** serves the endpoint, including the 400 for a string `endTime` and the 201 bodies.
- **Annotations** gain the `until_specific_time` and `permanent` hold types.
- **Tests:** unit tests against kumotest only. A live check on Office needs Hugh's agreement at that point.

## Left to the builder

Exact markup and CSS of the radio group, datastar wiring (follow the schedule editor's pattern), error text on a failed write.
