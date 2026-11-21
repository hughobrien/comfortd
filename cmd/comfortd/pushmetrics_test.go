// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/promwrite"
	"github.com/hughobrien/comfortd/internal/store"
)

type fakeWriter struct{ got [][]promwrite.Series }

func (w *fakeWriter) Write(_ context.Context, s []promwrite.Series) error {
	w.got = append(w.got, s)
	return nil
}

func TestPushUnitState(t *testing.T) {
	_, d := ctlDeps(t)
	targets := promwrite.Targets{{Name: "vm", URL: "http://vm"}}
	d.Metrics = NewMetrics(targets.Labels())
	w := &fakeWriter{}
	push := promwrite.NewPushers(targets, nil)
	push[0].Queue.W = w
	p := newMetricPusher(d.Metrics, d.Store, push, "comfortd", func() time.Duration { return time.Minute })
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()

	p.push(ctx, "office", false)
	if len(w.got) != 1 {
		t.Fatalf("want one write, got %d", len(w.got))
	}
	names := map[string]bool{}
	for _, s := range w.got[0] {
		if s.Labels["unit"] != "office" || s.Labels["job"] != "comfortd" || s.Labels["pushed_vm"] != "" || len(s.Samples) != 1 || !s.Samples[0].T.Equal(now) {
			t.Fatalf("series %v: want office only, job comfortd, no pushed_ mark, one sample at the push time", s)
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
	// Unit state stays on /metrics for any scraper, marked so vm's own scrape
	// can drop it; health is not marked.
	for _, want := range []string{
		`comfortd_mode{mode="heat",pushed_vm="true",unit="office"} 1`,
		`comfortd_room_temperature_celsius{pushed_vm="true",unit="office"}`,
		`comfortd_up{unit="office"}`,
		`comfortd_push_errors_total{target="vm"} 0`,
		`comfortd_push_queued_samples{target="vm"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s on /metrics:\n%s", want, body)
		}
	}
}

// stale lists the series in the writes whose sample is the staleness marker.
func stale(ws [][]promwrite.Series) map[string]bool {
	out := map[string]bool{}
	for _, w := range ws {
		for _, s := range w {
			for _, p := range s.Samples {
				if math.Float64bits(p.V) == 0x7ff0000000000002 {
					out[s.Labels["__name__"]+"/"+s.Labels["mode"]+s.Labels["unit"]] = true
				}
			}
		}
	}
	return out
}

func setMode(t *testing.T, st *store.Store, mode string) {
	t.Helper()
	_, err := st.Patch("office", store.EndpointDevice, func(old json.RawMessage) (json.RawMessage, error) {
		var m map[string]any
		if err := json.Unmarshal(old, &m); err != nil {
			return nil, err
		}
		m["operationMode"] = mode
		return json.Marshal(m)
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestPushMarksDisappearedSeriesStale(t *testing.T) {
	_, d := ctlDeps(t)
	targets := promwrite.Targets{{Name: "vm", URL: "http://vm"}}
	d.Metrics = NewMetrics(targets.Labels())
	w := &fakeWriter{}
	push := promwrite.NewPushers(targets, nil)
	push[0].Queue.W = w
	p := newMetricPusher(d.Metrics, d.Store, push, "comfortd", func() time.Duration { return time.Minute })
	ctx := context.Background()

	p.push(ctx, "office", false)
	if got := stale(w.got); len(got) != 0 {
		t.Fatalf("first push marked %v stale", got)
	}
	setMode(t, d.Store, "cool")
	p.push(ctx, "office", false)
	if got := stale(w.got); !got["comfortd_mode/heatoffice"] || len(got) != 1 {
		t.Fatalf("want only heat marked stale after heat to cool, got %v", got)
	}
	w.got = nil
	p.push(ctx, "office", false)
	if got := stale(w.got); len(got) != 0 {
		t.Errorf("a marker is sent once, got %v again", got)
	}
	if n := len(p.sent["office"]); n == 0 || n > 50 {
		t.Errorf("remembered %d series, want the current set only", n)
	}
}

// failOnce fails one write with a retryable error once failed is false.
type failOnce struct {
	fakeWriter
	failed bool
}

func (w *failOnce) Write(ctx context.Context, s []promwrite.Series) error {
	if !w.failed {
		w.failed = true
		return errors.New("down")
	}
	return w.fakeWriter.Write(ctx, s)
}

func TestStaleMarkersSurviveFailedPush(t *testing.T) {
	_, d := ctlDeps(t)
	targets := promwrite.Targets{{Name: "vm", URL: "http://vm"}}
	d.Metrics = NewMetrics(targets.Labels())
	w := &failOnce{failed: true} // the first push goes through
	push := promwrite.NewPushers(targets, nil)
	push[0].Queue.W = w
	p := newMetricPusher(d.Metrics, d.Store, push, "comfortd", func() time.Duration { return time.Minute })
	ctx := context.Background()

	p.push(ctx, "office", false)
	w.failed = false
	w.got = nil
	setMode(t, d.Store, "cool")
	p.push(ctx, "office", false) // fails: heat's marker stays queued
	if len(w.got) != 0 {
		t.Fatal("the write should have failed")
	}
	p.push(ctx, "office", false)
	if got := stale(w.got); !got["comfortd_mode/heatoffice"] {
		t.Errorf("marker lost by the failed push, got %v", got)
	}
}

func TestEncodeKeepsStaleNaN(t *testing.T) {
	b := promwrite.Encode([]promwrite.Series{{Labels: map[string]string{"__name__": "x"},
		Samples: []promwrite.Sample{{T: time.UnixMilli(1), V: staleNaN}}}})
	want := binary.LittleEndian.AppendUint64(nil, 0x7ff0000000000002)
	if !bytes.Contains(b, want) {
		t.Errorf("encoded sample lost the staleness bit pattern: %x", b)
	}
}
