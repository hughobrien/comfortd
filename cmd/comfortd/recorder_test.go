// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func openHist(t *testing.T) *history.Log {
	t.Helper()
	h, err := history.Open(filepath.Join(t.TempDir(), "history.jsonl"), history.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func TestNoRecordsOnFirstSight(t *testing.T) {
	h := openHist(t)
	err := recordUpdate(h, store.Update{Slug: "office", Endpoint: "device", New: json.RawMessage(`{"a":1}`), At: time.Now()}, nil)
	if err != nil || len(h.Recent("", 10)) != 0 {
		t.Fatalf("err=%v recs=%v", err, h.Recent("", 10))
	}
}

func TestRecordsDeviceChange(t *testing.T) {
	h := openHist(t)
	at := time.Now()
	err := recordUpdate(h, store.Update{Slug: "office", Endpoint: "device", At: at,
		Old: json.RawMessage(`{"spHeat":19,"x":"a"}`), New: json.RawMessage(`{"spHeat":21,"x":"a"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := h.Recent("office", 10)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	r := got[0]
	if r.Unit != "office" || r.Endpoint != "device" || r.Path != "spHeat" || r.Old != "19" || r.New != "21" || !r.T.Equal(at) {
		t.Fatalf("got %+v", r)
	}
}

func TestHistoryEndpoint(t *testing.T) {
	_, d, _ := polledDeps(t)
	d.History = openHist(t)
	base := time.Now()
	for i := range 1001 {
		d.History.Append(history.Record{T: base.Add(time.Duration(i) * time.Second), Unit: "office", Endpoint: "device", Path: "p", Old: "1", New: "2"})
	}
	h := newMux(d)
	var recs []history.Record
	rec := get(h, "/v1/history?unit=office&limit=1")
	if err := json.Unmarshal(rec.Body.Bytes(), &recs); err != nil || len(recs) != 1 || !recs[0].T.Equal(base.Add(1000*time.Second)) {
		t.Fatalf("err=%v recs=%v", err, recs)
	}
	rec = get(h, "/v1/history?limit=5000")
	recs = nil
	json.Unmarshal(rec.Body.Bytes(), &recs)
	if rec.Code != 200 || len(recs) > 1000 || len(recs) == 0 {
		t.Fatalf("code=%d len=%d", rec.Code, len(recs))
	}
	if rec := get(h, "/v1/history"); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func TestPollerToHistory(t *testing.T) {
	srv, d, _ := polledDeps(t)
	d.History = openHist(t)
	d.Store.Subscribe(func(u store.Update) { recordUpdate(d.History, u, nil) })
	// A second poller sharing the store is not available here, so re-poll via a fresh one.
	p := newPollerFor(t, srv, d.Store)
	p.PollOnce(context.Background())
	srv.SetField(kumotest.OfficeSerial, "spHeat", 23)
	p.PollOnce(context.Background())
	for _, r := range d.History.Recent("office", 50) {
		if r.Path == "spHeat" && r.New == "23" {
			return
		}
	}
	t.Fatalf("no spHeat record: %v", d.History.Recent("office", 50))
}

func newPollerFor(t *testing.T, srv *kumotest.Server, st *store.Store) *poller.Poller {
	t.Helper()
	creds := filepath.Join(t.TempDir(), "c")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	return poller.New(kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds}), st, time.Minute)
}
