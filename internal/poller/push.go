// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

const (
	queueSize       = 256
	catchUpInterval = time.Minute
)

// Push applies the Comfort app's Socket.IO push events to the store. Events
// are queued (a full queue drops the oldest, counted) and applied by one worker
// so the socket's reader never waits on the store, history or HomeKit.
type Push struct {
	st   *store.Store
	poll *Poller

	events       chan kumo.Event
	dropped      atomic.Int64
	connected    atomic.Bool
	echoVerified atomic.Bool // set once our own REST commands are known to echo on the socket
	holdDebounce time.Duration

	// OnHold is told, after the debounce and just before the hold is written to
	// the store, about a hold_update for slug (enabled is the pushed hold's state).
	OnHold func(slug string, enabled bool, at time.Time)
	// OnEventCount, when set, is told about every event applied (for metrics).
	OnEventCount func(name string)

	mu          sync.Mutex
	ctx         context.Context
	pendingHold map[string]*pendingHold
	lastCatchUp time.Time
}

type pendingHold struct {
	timer *time.Timer
	raw   json.RawMessage
}

func NewPush(st *store.Store, p *Poller) *Push {
	return &Push{
		st: st, poll: p,
		events:       make(chan kumo.Event, queueSize),
		holdDebounce: 150 * time.Millisecond,
		pendingHold:  map[string]*pendingHold{},
		ctx:          context.Background(),
	}
}

// Dropped is how many events were discarded because the queue was full.
func (x *Push) Dropped() int { return int(x.dropped.Load()) }

// Connected reports whether the socket session is up.
func (x *Push) Connected() bool { return x.connected.Load() }

// SetEchoVerified records that comfortd's own commands are known to echo on
// the socket, which lets Kick stand down while the socket is up.
func (x *Push) SetEchoVerified(v bool) { x.echoVerified.Store(v) }

// KickNeeded is false only when the socket is up and our commands are known to
// echo on it; otherwise the post-command polls are still the only confirmation.
func (x *Push) KickNeeded() bool { return !(x.echoVerified.Load() && x.connected.Load()) }

// OnEvent queues an event. It never blocks.
func (x *Push) OnEvent(e kumo.Event) {
	for {
		select {
		case x.events <- e:
			return
		default:
		}
		select {
		case <-x.events:
			x.dropped.Add(1)
		default:
		}
	}
}

// OnState is the socket's connect/disconnect callback. A (re)connect starts a
// catch-up poll (at most one a minute) for anything missed while it was down.
func (x *Push) OnState(up bool) {
	x.connected.Store(up)
	x.poll.Wake()
	if !up {
		return
	}
	x.mu.Lock()
	run := time.Since(x.lastCatchUp) >= catchUpInterval
	if run {
		x.lastCatchUp = time.Now()
	}
	ctx := x.ctx
	x.mu.Unlock()
	if run {
		go x.poll.PollOnce(ctx)
	}
}

// Run applies queued events until ctx is done.
func (x *Push) Run(ctx context.Context) {
	x.mu.Lock()
	x.ctx = ctx
	x.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-x.events:
			x.handle(e)
		}
	}
}

func (x *Push) slugFor(serial string) (string, bool) {
	for _, u := range x.st.Units() {
		if u.Serial == serial {
			return u.Slug, true
		}
	}
	return "", false
}

// handle applies one event. Unknown units, events that arrive before the first
// poll stored something to overlay on, and event kinds we do not use are
// ignored.
func (x *Push) handle(e kumo.Event) {
	var head struct {
		DeviceSerial string `json:"deviceSerial"`
	}
	if json.Unmarshal(e.Data, &head) != nil || head.DeviceSerial == "" {
		return
	}
	slug, ok := x.slugFor(head.DeviceSerial)
	if !ok {
		return
	}
	switch e.Name {
	case "device_update":
		x.count(e.Name)
		_, err := x.st.Patch(slug, store.EndpointDevice, func(old json.RawMessage) (json.RawMessage, error) {
			return OverlayDevice(old, e.Data)
		}, time.Now())
		if err != nil && err != errNothingStored {
			slog.Warn("push device_update", "unit", slug, "err", err)
		}
	case "hold_update":
		x.count(e.Name)
		x.queueHold(slug, e.Data)
	}
}

func (x *Push) count(name string) {
	if x.OnEventCount != nil {
		x.OnEventCount(name)
	}
}

// queueHold keeps only the last hold_update per unit within holdDebounce: the
// server sends two per change and their endTimes differ.
func (x *Push) queueHold(slug string, raw json.RawMessage) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if ph := x.pendingHold[slug]; ph != nil {
		ph.raw = raw
		ph.timer.Reset(x.holdDebounce)
		return
	}
	ph := &pendingHold{raw: raw}
	ph.timer = time.AfterFunc(x.holdDebounce, func() { x.applyHold(slug) })
	x.pendingHold[slug] = ph
}

func (x *Push) applyHold(slug string) {
	x.mu.Lock()
	ph := x.pendingHold[slug]
	delete(x.pendingHold, slug)
	x.mu.Unlock()
	if ph == nil {
		return
	}
	now := time.Now()
	// Tell the source tracker first: history is written inside Patch and needs
	// to know an app hold has just arrived.
	var h struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(ph.raw, &h)
	if x.OnHold != nil {
		x.OnHold(slug, h.Enabled, now)
	}
	if _, err := x.st.Patch(slug, store.EndpointZone, func(old json.RawMessage) (json.RawMessage, error) {
		return OverlayHold(old, ph.raw)
	}, now); err != nil && err != errNothingStored {
		slog.Warn("push hold_update", "unit", slug, "err", err)
	}
}
