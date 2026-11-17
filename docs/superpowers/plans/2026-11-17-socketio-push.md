# Socket.IO push Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Test first; one commit per task.

**Goal:** Replace comfortd's 120 s device poll with the Comfort app's Socket.IO push feed, with a 15 min backstop poll, and record the app as the source of changes it makes.

**Spec:** docs/superpowers/specs/2026-11-17-socketio-push-design.md (reviewed twice by Opus; binding).

**Tech Stack:** Go 1.26, `github.com/coder/websocket` (new), the existing store/poller/control packages.

## Global Constraints

- Nothing is sent to a real AC or the cloud; tests use `pkg/kumo/kumotest`. No serials, ids, tokens or addresses in fixtures or logs (public repo).
- `just gate` must print `NIX EXIT=0` before a push to main; the flake's Go vendor hash will change once (use the hash the gate prints).
- `Kick` keeps polling (flag `pushEchoVerified` stays false) until Hugh agrees a live echo check.
- Rollout keeps `-poll-interval 120s` for the first deploy.

## Review Focus

- A push that changes nothing must still advance `At`/`LastDeviceOK` and still let `ctl.Observe` confirm a sent field.
- Fast push after a REST body: the stored key set stays the REST one (no history spam from `date`, `payloadType`).
- Socket down: staleness window and backstop interval fall back to the poll interval.
- Duplicate `hold_update` (two per change, different `endTime`): one history row.

### Task 1: store.Patch and notify rules
Files: internal/store/store.go, store_test.go. Produces: `Patch(slug, endpoint string, fn func(old json.RawMessage) (json.RawMessage, error), at time.Time) (changed bool, err error)`; Put/Patch share a locked `put` helper; subscribers split into "always" (Observe) and "on change". Tests: unchanged Patch advances At/LastDeviceOK and calls only always-subscribers; changed Patch calls all; fn error leaves store untouched; Patch on an endpoint with no stored value passes nil old.

### Task 2: overlay functions (pure)
Files: internal/poller/overlay.go, overlay_test.go (+ redacted fixtures from the 2026-11-17 capture, placed under pkg/kumo/kumotest/fixtures). Produces: `OverlayDevice(old, push json.RawMessage) (json.RawMessage, error)` handling full and fast; `OverlayHold(old, push json.RawMessage) (json.RawMessage, error)`. Tests: key set preserved, push-only keys dropped, `displayConfigFast` mapped, nil old ignored, endTime format normalised to the REST format.

### Task 3: Engine.IO/Socket.IO codec
Files: pkg/kumo/sio_codec.go, test. Produces: `parsePacket(string) (Packet, error)`, `Packet.Encode()`; open/ping/pong/connect/connect_error/event/ack/disconnect. Table tests.

### Task 4: kumotest Socket.IO endpoint
Files: pkg/kumo/kumotest/socket.go, test. Handshake, `subscribe`/`subscribed`, ping, injectable 401 on upgrade and `44` reject, `PushDeviceUpdate(serial, full bool, body)` and `PushHold(...)`, hooks so `SetField`, send-command and the hold endpoint push automatically.

### Task 5: kumo.Socket
Files: pkg/kumo/socket.go, test. `Socket{...}.Run(ctx, serials, onEvent, onConn)`; waits for `40` before subscribing, pong replies, ping-timeout reconnect, backoff 1 s to 60 s, 30 s gap between forced token refreshes, 401 and `44` paths; counts `subscribed` replies. Tests against the fake.

### Task 6: push worker
Files: internal/poller/push.go, test. Buffered channel (256, drop oldest, counter), serial-to-slug, 150 ms per-unit hold debounce, overlay via Patch, `holdSeen` tracker update, debounced reconnect `PollOnce` in its own goroutine, `pushEchoVerified` flag gating Kick.

### Task 7: backstop and fallback
Files: internal/poller/poller.go, test. Run uses 15 min when the socket is connected and the old interval when not; profile/status 6 h unchanged.

### Task 8: staleness window
Files: store.go (no signature change), cmd/comfortd/server.go, metrics.go, ui/view.go, ui/card.go, internal/homekit/bridge.go, main.go. One injected `window func() time.Duration`: 3 min connected, 3 × poll interval otherwise.

### Task 9: history source
Files: internal/control/control.go, hold.go, cmd/comfortd/recorder.go, tests. `Controller.LastSent(slug)` set before the send; `recordUpdate(h, u, source)`; `source(slug, at, path)` returns `"app"` only for intent fields when an uncaused `hold_update` with `enabled` true arrived within 60 s. Table tests: app, ours, outside window, non-intent field, hold end.

### Task 10: wiring, metrics, e2e
Files: cmd/comfortd/main.go, metrics.go, tests. `-no-socket`; metrics `comfortd_socket_connected`, `comfortd_socket_events_total{event}`, `comfortd_socket_reconnects_total`, `comfortd_socket_dropped_total`; push-vs-poll disagreement log on power/mode/spHeat/spCool/fan/vane; an end-to-end `-fake` test where a fake change reaches the store and history via the socket.
