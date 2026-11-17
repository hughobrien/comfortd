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
- Handshake: after the websocket opens, wait for the server's open packet `0{...}`, send `40`, and only emit `subscribe` after the
  server's `40{sid}`. Two separate unauthorised paths: HTTP 401 on the upgrade and a `44` connect_error packet; both force one token
  refresh, bounded by a socket-side gap (30 s between forced refreshes; `authRejectGap` only gates failed logins). Each `subscribe` should be answered
  by `subscribed`; a missing reply is logged and counted.
- The reader goroutine does nothing slow: events go on a buffered channel to a worker that applies them, so history appends,
  HomeKit syncs and the catch-up `PollOnce` cannot make a pong miss `pingTimeout`.
- It sends nothing except `subscribe`, pongs and the namespace connect. It never reads or logs the token.

### `internal/poller` — becomes the refresher

- `Run`: initial full poll as today. Then **one ticker at 15 min** for the backstop (device, zone, MHK2, sensor, schedule) and the
  existing 6 h ticker for profile and status. The 60/120 s fast poll goes away; the `interval` argument becomes the backstop interval.
- Also a full `PollOnce` on every socket (re)connect, so anything missed while it was down is picked up. Debounced to once a minute.
- `Kick` **stays and keeps polling** until a live check proves our own REST command echoes on the socket (a flag, `pushEchoVerified`,
  false for now; Kick does nothing only when it is true and the socket is connected). Without the echo every command would time out as
  `unconfirmed`. Detail: (`control` calls it after every send and hold, and a sent field expires after `HoldTimeout`, 30 s, with an
  `unconfirmed` history record if no update confirms it). It polls at +3/+8/+15 s as now. Setting the flag needs a live check (Hugh's OK for one Office change); until then the kicks stay.
- Socket handlers (`internal/poller/push.go`) run on a **worker**, fed by a buffered channel (256). If the buffer is full the oldest
  event is dropped and counted (`comfortd_socket_dropped_total`); a later full `device_update` supersedes it anyway. The reconnect
  catch-up `PollOnce` runs in its own goroutine, never on the worker, so events keep flowing during a poll:
  - `device_update`, serial → slug via `store.Units()`:
    - Both shapes are applied as an **overlay onto the stored REST device JSON**, never stored as received, so the stored key set is
      always the REST one and history sees only real changes. Only keys already present in the stored map are overlaid (full:
      all of them; fast: `rssi`, `roomTemp`, `operationMode`, `power`, `spCool`, `spHeat`, `airDirection`, `fanSpeed`, `connected`;
      `displayConfigFast` maps onto `displayConfig`). `date`, `payloadType`, `scheduleOwner`, `lastStatusChangeAt` and other
      push-only keys are dropped. If nothing is stored yet, the event is ignored.
    - The overlay runs through a new atomic `store.Patch(slug, endpoint, func(old json.RawMessage) (json.RawMessage, error))` that
      holds the store lock across read and write, so it cannot overwrite a fresher poll (the hold patch lands just when
      `AfterHold`'s `PollOnce` does). `Patch` shares `Put`'s internals (a `put` helper called with the lock held: `ParseDevice`, the
      `ParseZones` wrap, setting `Device`/`Zone`/`LastDeviceOK`/`At`); `fn` must not call the store. A `hold_update` is ignored until
      a zone is stored, like a device push.
    - **The store still notifies subscribers on every Put/Patch** (`At[ep]` and `LastDeviceOK` must advance on an unchanged value,
      and `ctl.Observe` must confirm a sent field whose value equals the stored one, or it would go `unconfirmed` at 30 s). To avoid
      SSE and HomeKit churn, a push whose overlay changes nothing is handled by `Patch` returning "unchanged": it advances `At` and
      `LastDeviceOK` and notifies **nobody except `Observe`** (a separate, always-called subscriber slot); the other subscribers
      (history, hub, HomeKit) are called only when the JSON changed. Tests pin both behaviours.
    - The push `holdMode.endTime` may format differently from the REST zone's (ms precision, e.g. `...06:00:01.834Z`). A fixture of a
      real push decides; if the formats differ, the overlay normalises the push to the REST format so the 15 min poll records no fake change.
  - `hold_update`: patch the stored zone JSON's `holdMode` (`enabled`, `type`, `holdType`, `origin`, `endTime`, `id`, plus the held
    values; `deviceSerial` and `date` stripped) through `store.Patch`. The event arrives twice per change and the two are **not**
    identical (on a hold end the first `endTime` is a minute earlier), so patches are debounced ~150 ms per unit and only the last
    is applied.
  - `profile_update`: ignored (profile is polled every 6 h and rarely changes); `adapter_update` and the rest: ignored.
- Unknown serials are ignored.

### History source (issue 10)

`recordUpdate` records no source today. The one reliable signal is that **a change made in the app always opens a hold** (tested
2026-11-17, even a change back to the schedule's own value), and a `hold_update` precedes the matching device update by up to ~15 s.
So the rule is: a changed **intent field** (`operationMode`, `power`, `spHeat`, `spCool`, `spAuto`, `fanSpeed`, `airDirection`, and
`holdMode.*`) is recorded with `Source: "app"` only if a `hold_update` with `enabled` true arrived for that unit in the last 60 s
that comfortd did not cause. Everything else keeps an empty source: `roomTemp`, `rssi`, `connected`, `realValues` jitter, schedule
events firing, a hold expiring at its `endTime`, the wall thermostat and the IR remote. "Did comfortd cause it" uses a new
`Controller.LastSent(slug) time.Time`, set **before** the HTTP call in `send`, `SetHold` and `EndHold` (the echo can beat the HTTP
response; there is no pending-hold state today); a hold update within 60 s after our own send is ours. A shared tracker
(`holdSeen`: time of the last `hold_update` with `enabled` true per slug, written by the push worker) lives in `cmd/comfortd`;
`recordUpdate` receives `source func(slug string, at time.Time, path string) string` that reads the tracker and `LastSent`. An app
hold **end** (`enabled` false) is not tagged `app` (acceptable). Siri versus Home-app sliders cannot be told apart and are not.

### Wiring and operation

- `main.go`: build the socket from the client, start it as a worker beside the poller, pass the unit serials; a flag
  `-no-socket` (default off) disables it and falls back to polling at the old interval, for the fake and for emergencies.
- `-fake`: `kumotest` gains a Socket.IO endpoint that pushes whatever the fake changes (device change, hold change) so the daemon
  runs the same path end to end offline. `-no-socket` is for emergencies and for tests of the polling path.
- Metrics: `comfortd_socket_connected` gauge and `comfortd_socket_events_total{event}`, `comfortd_socket_reconnects_total`.
- Staleness: `Snapshot.Stale` is `3 × interval` today, and the interval becomes 15 min, which would hide a dead socket for 45 min.
  Staleness gets its own window function, `window func() time.Duration`, (3 × 2 min while the socket is connected: the capture saw `device_update` for a quiet unit every ~10 to 15 s; 3 × the poll interval
  while it is down or with `-no-socket`; the poller returns to the old interval while the socket is down, so a short window never
  sits against a 15 min backstop). `Snapshot.Stale(now, interval)` keeps its signature; its callers (server.go, metrics.go,
  ui/view.go, ui/card.go, homekit/bridge.go, `Options.Interval`) pass `window()` instead of the poll interval, measured from the last `device_update` of
  any shape, or from the last poll when the socket is down for more than a minute. HomeKit, the UI and the metrics read the new
  window; they no longer take the poll interval.
- Churn: the store only notifies subscribers when a `Patch`/`Put` changes the JSON, so an unchanged overlay does not trigger
  `hub.Publish` or `bridge.Sync`.
- Rollout: first deploy runs the socket **and** the old 120 s poll (`-poll-interval 120s` stays); compare for a day (a log line
  when a pushed device body disagrees with the next poll for `power`, `operationMode`, `spHeat`, `spCool`, `fanSpeed` or
  `airDirection`), then set the backstop to 15 min. Then, with Hugh's OK for one Office change, check that comfortd's own REST
  command echoes on the socket, and only then retire the kicks.

## Built differently from the first draft

- Stale window while connected is 6 min (3 × a 2 min beat): the 2026-11-17 capture showed a quiet unit going 40 s between
  `device_update`s, so 3 min was too tight.
- `device_status_v2` (adapter connected/disconnected) is not applied yet; its payload is only known from the decompile. A dead
  adapter shows through silence (stale after 6 min) until that is added.
- `-no-socket`, `-socket-url`, `-backstop` (env `COMFORTD_BACKSTOP`) are the new flags.

## Constraints

- No command goes to a real AC from this work. The socket is read-only; tests use the fake. The live check uses the Mac listener's
  proven shape against the real server (subscribe only).
- Public repo: no serials, ids or tokens in fixtures, logs or docs. Fixtures come from redacted real events.
- Dependency: `github.com/coder/websocket` (pure Go, no dependencies). `flake.nix`'s Go vendor hash changes; the gate shows the new hash.
- Library versions: newest stable.

## Left to the implementer

- Exact reconnect timings, the fast-merge key list (as above), file layout inside `pkg/kumo`, metric label detail.
- Names and file layout beyond those given.
