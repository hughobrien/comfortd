// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func TestMissedChanges(t *testing.T) {
	old := json.RawMessage(`{"spHeat":20,"roomTemp":20,"fanSpeed":"auto","power":1}`)
	new := json.RawMessage(`{"spHeat":21,"roomTemp":21,"fanSpeed":"auto","power":0}`)
	got := missedChanges(old, new)
	if len(got) != 2 || got[0] != "power" || got[1] != "spHeat" {
		t.Fatalf("missed = %v, want only the intent fields that changed, sorted", got)
	}
	if len(missedChanges(nil, new)) != 0 {
		t.Fatal("first sight is not a miss")
	}
}

// The whole path: a change in the fake cloud reaches the store through the
// socket with the poll interval far too long to have done it.
func TestPushWiringEndToEnd(t *testing.T) {
	srv := kumotest.New()
	defer srv.Close()
	dir := t.TempDir()
	creds := filepath.Join(dir, "c")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	client := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds})
	ctx, cancel := context.WithCancel(context.Background())
	units, err := poller.Discover(ctx, client, filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(units)
	p := poller.New(client, st, time.Hour)
	p.PollOnce(ctx)
	var wg sync.WaitGroup
	m := NewMetrics()
	pw := wirePush(pushOptions{
		Client: client, Store: st, Poller: p, SocketURL: srv.URL, Backstop: time.Hour, PollInterval: time.Hour,
		Metrics: m, LastSent: func(string) time.Time { return time.Time{} },
		Go: func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() },
	}, ctx)
	defer func() { cancel(); wg.Wait() }()

	waitUntilMain(t, func() bool { return pw.Push.Connected() })
	if got := pw.Beat(); got != pushBeat {
		t.Fatalf("beat while connected = %v, want %v", got, pushBeat)
	}
	srv.SetField(kumotest.OfficeSerial, "spHeat", 22.5)
	waitUntilMain(t, func() bool {
		sn, _ := st.Get("office")
		return sn.Device != nil && sn.Device.SpHeat != nil && *sn.Device.SpHeat == 22.5
	})
	srv.DisconnectSockets()
	waitUntilMain(t, func() bool { return !pw.Push.Connected() })
	if got := pw.Beat(); got != time.Hour {
		t.Fatalf("beat while down = %v, want the poll interval", got)
	}
}

func waitUntilMain(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
