// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func ev(name, body string) kumo.Event { return kumo.Event{Name: name, Data: json.RawMessage(body)} }

func fastBody(serial string, spHeat float64) string {
	return fmt.Sprintf(`{"deviceSerial":%q,"payloadType":"fast","date":"2026-11-17T22:15:26.942Z","spHeat":%v,"connected":true}`, serial, spHeat)
}

func newPush(t *testing.T) (*kumotest.Server, *store.Store, *Poller, *Push) {
	srv, st, p := newPoller(t)
	p.PollOnce(context.Background())
	x := NewPush(st, p)
	x.holdDebounce = 30 * time.Millisecond
	return srv, st, p, x
}

func TestPushFastUpdatePatchesDevice(t *testing.T) {
	_, st, _, x := newPush(t)
	before, _ := st.Get("office")
	x.handle(ev("device_update", fastBody(kumotest.OfficeSerial, 23)))
	after, _ := st.Get("office")
	if after.Device.SpHeat == nil || *after.Device.SpHeat != 23 {
		t.Fatalf("spHeat = %v", after.Device.SpHeat)
	}
	var a, b map[string]any
	json.Unmarshal(before.Raw[store.EndpointDevice], &a)
	json.Unmarshal(after.Raw[store.EndpointDevice], &b)
	if len(a) != len(b) {
		t.Fatalf("stored key set changed: %d -> %d", len(a), len(b))
	}
}

func TestPushUnchangedAdvancesTimeAndStaysQuiet(t *testing.T) {
	_, st, _, x := newPush(t)
	sn, _ := st.Get("office")
	same := *sn.Device.SpHeat
	var calls int
	st.Subscribe(func(store.Update) { calls++ })
	time.Sleep(2 * time.Millisecond)
	x.handle(ev("device_update", fastBody(kumotest.OfficeSerial, same)))
	after, _ := st.Get("office")
	if calls != 0 {
		t.Fatalf("subscribers called %d times for an unchanged push", calls)
	}
	if !after.LastDeviceOK.After(sn.LastDeviceOK) {
		t.Fatal("LastDeviceOK did not advance")
	}
}

func TestPushIgnoresUnknownSerialsAndEarlyEvents(t *testing.T) {
	srv, c, cache := setup(t)
	_ = srv
	st := store.New(discovered(t, c, cache))
	x := NewPush(st, New(c, st, time.Minute))
	x.handle(ev("device_update", fastBody("NOSUCHSERIAL", 20)))                                // unknown unit
	x.handle(ev("device_update", fastBody(kumotest.OfficeSerial, 20)))                         // nothing stored yet
	x.handle(ev("hold_update", `{"deviceSerial":"`+kumotest.OfficeSerial+`","enabled":true}`)) // no zone stored
	x.handle(ev("profile_update", `{}`))
	x.handle(ev("device_update", `not json`))
	if sn, _ := st.Get("office"); sn.Raw[store.EndpointDevice] != nil {
		t.Fatal("an early push must not create a device body")
	}
}

func holdBody(serial string, enabled bool, endTime string) string {
	return fmt.Sprintf(`{"deviceSerial":%q,"date":"2026-11-17T22:16:40.972Z","id":"h","enabled":%v,"type":"hold","holdType":"until_next_event","origin":"adapter","endTime":%q}`, serial, enabled, endTime)
}

func TestPushHoldDebouncedToTheLastEvent(t *testing.T) {
	_, st, _, x := newPush(t)
	var mu sync.Mutex
	var seen []bool
	x.OnHold = func(slug string, enabled bool, at time.Time) { mu.Lock(); seen = append(seen, enabled); mu.Unlock() }
	var zoneUps int
	st.Subscribe(func(u store.Update) {
		if u.Endpoint == store.EndpointZone {
			mu.Lock()
			zoneUps++
			mu.Unlock()
		}
	})
	x.handle(ev("hold_update", holdBody(kumotest.OfficeSerial, false, "2026-11-17T22:16:00.402Z")))
	x.handle(ev("hold_update", holdBody(kumotest.OfficeSerial, false, "2026-11-17T22:17:00.438Z")))
	waitUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 })
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || zoneUps != 1 {
		t.Fatalf("seen=%v zoneUps=%d, want one of each", seen, zoneUps)
	}
	sn, _ := st.Get("office")
	var z struct{ HoldMode struct{ EndTime string } }
	json.Unmarshal(sn.Raw[store.EndpointZone], &z)
	if z.HoldMode.EndTime != "2026-11-17T22:17:00.438Z" {
		t.Fatalf("endTime = %q, want the later event's", z.HoldMode.EndTime)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestPushQueueDropsOldest(t *testing.T) {
	_, _, _, x := newPush(t)
	for i := 0; i < queueSize+44; i++ {
		x.OnEvent(ev("device_update", fmt.Sprintf(`{"n":%d}`, i)))
	}
	if x.Dropped() != 44 {
		t.Fatalf("dropped = %d, want 44", x.Dropped())
	}
	first := <-x.events
	if string(first.Data) != `{"n":44}` {
		t.Fatalf("oldest kept = %s", first.Data)
	}
}

func TestPushConnectTriggersOneCatchUpPoll(t *testing.T) {
	srv, _, _, x := newPush(t)
	path := "/devices/" + kumotest.OfficeSerial
	before := srv.Requests(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x.OnState(true)
	x.OnState(false)
	x.OnState(true)
	_ = ctx
	waitUntil(t, func() bool { return srv.Requests(path) > before })
	time.Sleep(50 * time.Millisecond)
	if got := srv.Requests(path) - before; got != 1 {
		t.Fatalf("catch-up polls = %d, want 1 (debounced)", got)
	}
	if !x.Connected() {
		t.Fatal("Connected() should be true")
	}
}

func TestKickGatedByPushEcho(t *testing.T) {
	srv, _, p, x := newPush(t)
	p.kickDelays = []time.Duration{5 * time.Millisecond}
	p.KickNeeded = x.KickNeeded
	path := "/devices/" + kumotest.OfficeSerial
	x.connected.Store(true)
	before := srv.Requests(path)
	p.Kick("office") // echo not verified: still polls
	waitUntil(t, func() bool { return srv.Requests(path) > before })
	x.echoVerified.Store(true)
	before = srv.Requests(path)
	p.Kick("office")
	time.Sleep(60 * time.Millisecond)
	if srv.Requests(path) != before {
		t.Fatal("Kick should do nothing once the echo is verified and the socket is up")
	}
	x.connected.Store(false)
	p.Kick("office")
	waitUntil(t, func() bool { return srv.Requests(path) > before })
}

func TestBackstopIntervalDependsOnSocket(t *testing.T) {
	srv, _, p := newPoller(t)
	p.interval = 15 * time.Millisecond
	var up bool
	var mu sync.Mutex
	p.UseSocket(func() bool { mu.Lock(); defer mu.Unlock(); return up }, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := "/devices/" + kumotest.OfficeSerial
	mu.Lock()
	up = true
	mu.Unlock()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	time.Sleep(150 * time.Millisecond)
	connected := srv.Requests(path)
	if connected > 6 { // start-up polls only
		t.Fatalf("polls while connected = %d, want only the start-up ones", connected)
	}
	mu.Lock()
	up = false
	mu.Unlock()
	p.Wake()
	time.Sleep(150 * time.Millisecond)
	if srv.Requests(path) < connected+4 {
		t.Fatalf("polls after the socket dropped = %d (was %d), want the fast interval back", srv.Requests(path), connected)
	}
	cancel()
	<-done
}
