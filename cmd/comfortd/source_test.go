// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/store"
)

func TestSourceTracker(t *testing.T) {
	t0 := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		hold     time.Duration // hold_update(enabled) seen this long after t0; <0 = never
		sent     time.Duration // our last send this long after t0; <-1h = never
		at       time.Duration // the change is recorded this long after t0
		endpoint string
		path     string
		old, new string
		want     string
	}{
		{"app setpoint after its hold", 0, -2 * time.Hour, 14 * time.Second, "device", "spHeat", "20", "20.5", "app"},
		{"app hold start", 0, -2 * time.Hour, 0, "zone", "holdMode.enabled", "false", "true", "app"},
		{"setpoint with no hold seen", -1, -2 * time.Hour, 5 * time.Second, "device", "spHeat", "20", "21", ""},
		{"schedule event firing is not the app", -1, -2 * time.Hour, 5 * time.Second, "device", "operationMode", `"heat"`, `"off"`, ""},
		{"room temp jitter", 0, -2 * time.Hour, 5 * time.Second, "device", "roomTemp", "20", "20.5", ""},
		{"our own hold", 0, -time.Second, time.Second, "zone", "holdMode.enabled", "false", "true", ""},
		{"our hold, its echo lands later", 0, -30 * time.Second, 20 * time.Second, "device", "spHeat", "20", "21", ""},
		{"app change long after our send", 0, -3 * time.Minute, time.Second, "device", "fanSpeed", `"auto"`, `"quiet"`, "app"},
		{"our send after the app's hold", 0, 10 * time.Second, 12 * time.Second, "device", "spHeat", "20", "21", ""},
		{"hold seen too long ago", 0, -2 * time.Hour, 2 * time.Minute, "device", "spHeat", "20", "21", ""},
		{"app hold end is not tagged", 0, -2 * time.Hour, 5 * time.Second, "zone", "holdMode.enabled", "true", "false", ""},
		{"hold fields other than enabled", 0, -2 * time.Hour, 0, "zone", "holdMode.endTime", `"a"`, `"b"`, ""},
	}
	for _, c := range cases {
		var tr sourceTracker
		tr.lastSent = func(string) time.Time {
			if c.sent < -time.Hour {
				return time.Time{}
			}
			return t0.Add(c.sent)
		}
		if c.hold >= 0 {
			tr.HoldPushed("office", true, t0.Add(c.hold))
		}
		if got := tr.source("office", c.endpoint, c.path, c.old, c.new, t0.Add(c.at)); got != c.want {
			t.Errorf("%s: source = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSourceTrackerIgnoresHoldEnds(t *testing.T) {
	var tr sourceTracker
	tr.lastSent = func(string) time.Time { return time.Time{} }
	tr.HoldPushed("office", false, time.Now())
	if got := tr.source("office", "device", "spHeat", "1", "2", time.Now()); got != "" {
		t.Fatalf("a hold end must not mark later changes as the app's: %q", got)
	}
}

func TestRecordUpdateUsesSource(t *testing.T) {
	h, err := history.Open(filepath.Join(t.TempDir(), "h.jsonl"), history.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	at := time.Now()
	src := func(slug, endpoint, path, old, new string, _ time.Time) string {
		if path == "spHeat" {
			return "app"
		}
		return ""
	}
	err = recordUpdate(h, store.Update{Slug: "office", Endpoint: "device", At: at,
		Old: json.RawMessage(`{"spHeat":20,"roomTemp":20}`), New: json.RawMessage(`{"spHeat":21,"roomTemp":21}`)}, src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range h.Recent("office", 10) {
		got[r.Path] = r.Source
	}
	if got["spHeat"] != "app" || got["roomTemp"] != "" || len(got) != 2 {
		t.Fatalf("records = %v", got)
	}
}
