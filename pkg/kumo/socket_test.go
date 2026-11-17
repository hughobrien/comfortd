// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

type sockRec struct {
	mu     sync.Mutex
	events []Event
	states []bool
}

func (r *sockRec) onEvent(e Event) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() }
func (r *sockRec) onState(v bool)  { r.mu.Lock(); r.states = append(r.states, v); r.mu.Unlock() }
func (r *sockRec) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Name == name {
			n++
		}
	}
	return n
}
func (r *sockRec) stateList() []bool { r.mu.Lock(); defer r.mu.Unlock(); return slices.Clone(r.states) }
func (r *sockRec) find(name string, pred func(map[string]any) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Name != name {
			continue
		}
		var m map[string]any
		if json.Unmarshal(e.Data, &m) == nil && pred(m) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startSocket(t *testing.T, e *env) (*Socket, *sockRec) {
	t.Helper()
	rec := &sockRec{}
	s := e.client().NewSocket(SocketOptions{
		URL:        e.srv.URL,
		Serials:    []string{kumotest.OfficeSerial, kumotest.HouseSerial},
		OnEvent:    rec.onEvent,
		OnState:    rec.onState,
		MinBackoff: 5 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
		RefreshGap: time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, rec
}

func TestSocketConnectsSubscribesAndReceivesSnapshot(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	s, rec := startSocket(t, e)
	waitFor(t, "snapshots", func() bool { return rec.count("device_update") >= 2 })
	if got := e.srv.SocketSubscribed(); len(got) != 2 {
		t.Fatalf("subscribed = %v", got)
	}
	waitFor(t, "state", func() bool { return len(rec.stateList()) == 1 && rec.stateList()[0] })
	waitFor(t, "subscribed replies", func() bool { return s.Subscribed() == 2 })
}

func TestSocketCountsMissingSubscribedReplies(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	s, _ := startSocket(t, e)
	waitFor(t, "subscribed", func() bool { return s.Subscribed() == 2 })
	if s.Expected() != 2 {
		t.Fatalf("expected = %d", s.Expected())
	}
}

func TestSocketDeliversPushes(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	_, rec := startSocket(t, e)
	waitFor(t, "connected", func() bool { return e.srv.SocketClients() == 1 })
	e.srv.SetField(kumotest.OfficeSerial, "spHeat", 22.0)
	waitFor(t, "fast push", func() bool {
		return rec.find("device_update", func(m map[string]any) bool { return m["payloadType"] == "fast" && m["spHeat"] == 22.0 })
	})
	e.srv.PushHoldUpdate(kumotest.OfficeZoneID) // nothing held yet: no event, no crash
}

func TestSocketReconnectsAndResubscribes(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	s, rec := startSocket(t, e)
	waitFor(t, "first subscribes", func() bool { return len(e.srv.SocketSubscribed()) == 2 })
	e.srv.DisconnectSockets()
	waitFor(t, "second connect", func() bool { return e.srv.SocketConnects() == 2 && e.srv.SocketClients() == 1 })
	waitFor(t, "resubscribed", func() bool { return len(e.srv.SocketSubscribed()) == 4 })
	waitFor(t, "state sequence", func() bool {
		st := rec.stateList()
		return len(st) >= 3 && st[0] && !st[1] && st[2]
	})
	if s.Reconnects() < 1 {
		t.Fatalf("reconnects = %d", s.Reconnects())
	}
}

func TestSocketReconnectsWhenServerGoesSilent(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	e.srv.SetPing(30*time.Millisecond, 30*time.Millisecond)
	startSocket(t, e)
	waitFor(t, "first connect", func() bool { return e.srv.SocketConnects() == 1 })
	e.srv.SilenceServer(true)
	waitFor(t, "reconnect after ping timeout", func() bool { return e.srv.SocketConnects() >= 2 })
}

func TestSocketRefreshesTokenOnUpgrade401(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	e.srv.RejectUpgrade(401, 1)
	startSocket(t, e)
	waitFor(t, "connect after 401", func() bool { return e.srv.SocketClients() == 1 })
	if e.srv.Refreshes() < 1 {
		t.Fatalf("refreshes = %d, want a forced refresh", e.srv.Refreshes())
	}
}

func TestSocketRefreshesTokenOnConnectError(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	e.srv.RejectConnect(1)
	startSocket(t, e)
	waitFor(t, "connect after 44", func() bool { return e.srv.SocketClients() == 1 })
	if e.srv.Refreshes() < 1 {
		t.Fatalf("refreshes = %d, want a forced refresh", e.srv.Refreshes())
	}
}

func TestSocketDialErrorSaysWhy(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	s := e.client().NewSocket(SocketOptions{URL: "http://127.0.0.1:1"}) // nothing listens there
	err := s.session(context.Background())
	if err == nil || err.Error() == "dial failed" {
		t.Fatalf("err = %v, want the underlying cause", err)
	}
	if strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("error leaks the header: %v", err)
	}
}
