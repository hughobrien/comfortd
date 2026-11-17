# Socket.IO push instead of polling — design

Issues: 11 (the app's API), 10 (history should show the app as the source).

## Why

comfortd polls `/devices/{serial}` and the site's zones every poll interval (the daemon default is 60 s; bigthink runs 120 s) and
re-polls after each command at +3, +8 and +15 s. Hugh wants the poll gone. Live tests on 2026-11-17 (docs/api-inventory.md,
"Socket.IO observed live") showed the app's own change feed works with the same Bearer token: after `subscribe <serial>` the
server pushes `device_update` (full and fast shapes), `hold_update`, `profile_update` and `adapter_update` within about a second
of a change. This gives instant state, instant holds, and a way to tell the app's changes from ours (issue 10).

## Goals

- State changes (device, hold) reach the store within seconds, with no 120 s wait.
- The fast poll and the +3/+8/+15 s kicks are removed.
- History records `app` as the source for a change comfortd did not make.
- If the socket is down or silent, the data still refreshes (the backstop below) and the UI says so the way it does today.

## Non-goals

- Sending through the socket (commands stay REST). No `force_adapter_request` yet.
- Zones, schedule, MHK2, sensor and status endpoints are not pushed (no event seen for them); they stay polled.
- No change to the store's raw-JSON model, the UI, HomeKit, metrics or the history format.

## Approaches

1. **Minimal own client** (chosen). A small Engine.IO v4 / Socket.IO client in `pkg/kumo` on `github.com/coder/websocket`. No Go
   Socket.IO library is maintained to a standard worth importing; the wire format is a few message types (open `0`, ping `2` /
   pong `3`, connect `40`, event `42[...]`, ack `43`, disconnect `41`).
2. A full third-party Socket.IO Go library: heavier, v4-protocol support is patchy, and it pulls many dependencies.
3. Keep polling and make it faster: no push, more API load, no source information.

## Design

### `pkg/kumo/socket.go` — the client

`type Socket` with `Run(ctx, serials []string, onEvent func(Event))`:

- `Event{Name string; Data json.RawMessage}` (first argument of the event only).
- Connects to `wss://socket-prod.kumocloud.com/socket.io/?EIO=4&transport=websocket` with `Authorization: Bearer <access>`
  (token from the existing `Client.token`; the base URL of the socket is an Option so tests and the fake can point elsewhere).
- Handles the handshake, answers pings, emits `subscribe <serial>` for each serial after every (re)connect, reads events.
- On any read error or a ping timeout (no server ping for `pingInterval + pingTimeout`), closes and reconnects with backoff
  (1 s doubling to 60 s, reset after a connection that lasted a minute). A connect rejected as unauthorised forces a token refresh
  (`token(ctx, true)`) before the retry.
- It reports `connected`/`disconnected` through callbacks so the daemon can run a catch-up refresh and expose a metric.
- It sends nothing except `subscribe`, pongs and the namespace connect. It never reads or logs the token.

### `internal/poller` — becomes the refresher

- `Run`: initial full poll as today. Then **one ticker at 15 min** for the backstop (device, zone, MHK2, sensor, schedule) and the
  existing 6 h ticker for profile and status. The 60/120 s fast poll goes away; the `interval` argument becomes the backstop interval.
- Also a full `PollOnce` on every socket (re)connect, so anything missed while it was down is picked up. Debounced to once a minute.
- `Kick` goes away (its callers in `control` use `Options.Kick`, which main wires to nothing; the fake keeps the +3 s kick shape
  for the polling-only path used by `-fake` without the socket).
- Socket handlers (`internal/poller/push.go`), run on the socket goroutine:
  - `device_update`, serial → slug via `store.Units()`:
    - `payloadType == "full"`: `store.Put(slug, device, raw, now)` (the full body has the same fields as the REST device, plus
      extras; `kumo.ParseDevice` ignores unknown fields).
    - `"fast"`: unmarshal the current stored device JSON into a map, overlay the fast body's keys that exist in the stored map
      (`rssi`, `roomTemp`, `operationMode`, `power`, `spCool`, `spHeat`, `airDirection`, `fanSpeed`, `realValues`, `connected`)
      and drop `displayConfigFast`, `payloadType`, `date`; store the result. If nothing is stored yet, ignore it.
  - `hold_update`: patch the stored zone JSON's `holdMode` (`enabled`, `type`, `holdType`, `origin`, `endTime`, `id`, plus the held
    values) from the event. The event arrives twice per change; identical patches change nothing and record no history.
  - `profile_update`: ignored (profile is polled every 6 h and rarely changes); `adapter_update` and the rest: ignored.
- Unknown serials are ignored.

### History source (issue 10)

`recordUpdate` currently records no source. A push-driven change is the app's unless comfortd caused it. The controller already
tracks pending commands per unit and a pending hold per unit. New: `Controller.Expecting(slug) bool`, true while a command or hold
for that unit is pending or was sent within the last 20 s (grace for the echo). `recordUpdate` takes an `expecting func(slug) bool`;
changes recorded while it is false get `Source: "app"`, otherwise empty (the controller already writes its own `command`/`hold`
records with the real source). Polled catch-up changes recorded at reconnect use the same rule. Siri versus Home-app sliders cannot
be told apart and are not.

### Wiring and operation

- `main.go`: build the socket from the client, start it as a worker beside the poller, pass the unit serials; a flag
  `-no-socket` (default off) disables it and falls back to polling at the old interval, for the fake and for emergencies.
- `-fake`: `kumotest` gains a Socket.IO endpoint that pushes whatever the fake sends (device change, hold change) so the daemon runs the
  same path end to end offline.
- Metrics: `comfortd_socket_connected` gauge and `comfortd_socket_events_total{event}`, `comfortd_socket_reconnects_total`.
- The UI's stale marker keeps working: `Snapshot.Stale` uses the last device update time; a quiet unit still gets the full
  `device_update` snapshot every ~12 s while the socket is up (seen live), so a connected socket keeps `LastDeviceOK` fresh. If
  the socket is down the 15 min backstop is the only refresh, and the UI marks it stale as it does now.
- Rollout: first deploy runs the socket **and** the old 120 s poll (`-poll-interval 120s` stays); compare for a day (a log line
  when a pushed device body disagrees with the next poll for the same field), then flip the interval to 15 min.

## Constraints

- No command goes to a real AC from this work. The socket is read-only; tests use the fake. The live check uses the Mac listener's
  proven shape against the real server (subscribe only).
- Public repo: no serials, ids or tokens in fixtures, logs or docs. Fixtures come from redacted real events.
- Dependency: `github.com/coder/websocket` (pure Go, no dependencies). `flake.nix`'s Go vendor hash changes; the gate shows the new hash.
- Library versions: newest stable.

## Left to the implementer

- Exact reconnect timings, the fast-merge key list (as above), file layout inside `pkg/kumo`, metric label detail.
- How `Expecting` is computed from the controller's existing pending state.
