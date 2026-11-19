// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/promwrite"
)

type fakeWriter struct{ got [][]promwrite.Series }

func (w *fakeWriter) Write(_ context.Context, s []promwrite.Series) error {
	w.got = append(w.got, s)
	return nil
}

func TestPushUnitState(t *testing.T) {
	_, d := ctlDeps(t)
	d.Metrics = NewMetrics(true)
	w := &fakeWriter{}
	p := newMetricPusher(d.Metrics, d.Store, w, "comfortd", func() time.Duration { return time.Minute })
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()

	p.push(ctx, "office", false)
	if len(w.got) != 1 {
		t.Fatalf("want one write, got %d", len(w.got))
	}
	names := map[string]bool{}
	for _, s := range w.got[0] {
		if s.Labels["unit"] != "office" || s.Labels["job"] != "comfortd" || len(s.Samples) != 1 || !s.Samples[0].T.Equal(now) {
			t.Fatalf("series %v: want office only, job comfortd, one sample at the push time", s)
		}
		names[s.Labels["__name__"]] = true
	}
	for _, n := range []string{"comfortd_room_temperature_celsius", "comfortd_setpoint_celsius", "comfortd_mode", "comfortd_power"} {
		if !names[n] {
			t.Errorf("%s not pushed (got %v)", n, names)
		}
	}
	if names["comfortd_up"] || names["comfortd_last_poll_timestamp_seconds"] {
		t.Error("health stays on /metrics, not pushed")
	}

	p.push(ctx, "office", false) // same instant: must still be newer
	if s := w.got[1][0].Samples[0]; !s.T.Equal(now.Add(time.Millisecond)) {
		t.Errorf("second push at %v, want 1ms after the first", s.T)
	}

	p.push(ctx, "office", true) // a fresh unit is resent
	if len(w.got) != 3 {
		t.Fatal("a fresh unit should be resent")
	}
	now = now.Add(time.Hour) // three intervals without a device poll: stale
	p.push(ctx, "office", true)
	if len(w.got) != 3 {
		t.Error("a stale unit should not be resent")
	}

	rec := httptest.NewRecorder()
	newMux(d).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if strings.Contains(body, "comfortd_mode{") || strings.Contains(body, "comfortd_room_temperature_celsius{") {
		t.Errorf("pushed gauges should not be on /metrics:\n%s", body)
	}
	for _, want := range []string{`comfortd_up{unit="office"}`, "comfortd_push_errors_total 0", "comfortd_push_queued_samples 0"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s on /metrics", want)
		}
	}
}
