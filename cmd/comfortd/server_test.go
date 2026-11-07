// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func polledDeps(t *testing.T) (*kumotest.Server, deps, *time.Time) {
	t.Helper()
	srv := kumotest.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "c")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	c := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds})
	units, err := poller.Discover(context.Background(), c, filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(units)
	poller.New(c, st, time.Minute).PollOnce(context.Background())
	now := time.Now()
	return srv, deps{Store: st, Interval: time.Minute, Now: func() time.Time { return now }}, &now
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestUnitsEndpoint(t *testing.T) {
	_, d, _ := polledDeps(t)
	rec := get(newMux(d), "/v1/units")
	var units []struct {
		Slug  string
		Stale bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &units); err != nil || len(units) != 2 {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	for _, u := range units {
		if u.Stale {
			t.Fatalf("%s stale", u.Slug)
		}
	}
}

func TestRawEndpoint(t *testing.T) {
	srv, d, _ := polledDeps(t)
	h := newMux(d)
	rec := get(h, "/v1/units/office/raw/device")
	var got, want any
	json.Unmarshal(rec.Body.Bytes(), &got)
	b, _ := json.Marshal(srv.Device(kumotest.OfficeSerial))
	json.Unmarshal(b, &want)
	if rec.Code != 200 || !reflect.DeepEqual(got, want) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if c := get(h, "/v1/units/nope/raw/device").Code; c != 404 {
		t.Fatalf("unknown slug = %d", c)
	}
	if c := get(h, "/v1/units/office/raw/bogus").Code; c != 404 {
		t.Fatalf("unknown endpoint = %d", c)
	}
	rec = get(h, "/v1/units/office/raw/sensor")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "not fetched") {
		t.Fatalf("unfetched = %d %s", rec.Code, rec.Body)
	}
}

func TestHealthz(t *testing.T) {
	_, d, now := polledDeps(t)
	h := newMux(d)
	rec := get(h, "/healthz")
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Fatalf("fresh = %d %q", rec.Code, rec.Body)
	}
	*now = now.Add(4 * time.Minute)
	rec = get(h, "/healthz")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "office") {
		t.Fatalf("stale = %d %q", rec.Code, rec.Body)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// TestRunFakeServes also checks that startup logs contain no secrets.
func TestRunFakeServes(t *testing.T) {
	logs := &lockedBuf{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan net.Addr, 1)
	done := make(chan int, 1)
	go func() {
		done <- runContext(ctx, []string{"-fake", "-listen", "127.0.0.1:0", "-state-dir", t.TempDir()}, io.Discard,
			func(a net.Addr) { addrCh <- a })
	}()
	var addr net.Addr
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://" + addr.String() + "/v1/units")
		if err == nil {
			var units []map[string]any
			json.NewDecoder(resp.Body).Decode(&units)
			resp.Body.Close()
			if len(units) == 2 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw 2 units")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not shut down")
	}

	out := logs.String()
	if !strings.Contains(out, "listening") {
		t.Fatalf("no listening log: %s", out)
	}
	for _, bad := range []string{kumotest.Password, "access-", "refresh-", "Bearer"} {
		if strings.Contains(out, bad) {
			t.Fatalf("logs contain %q:\n%s", bad, out)
		}
	}
}
