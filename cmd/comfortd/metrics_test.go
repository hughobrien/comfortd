// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsScrape(t *testing.T) {
	_, d := ctlDeps(t)
	d.Metrics = NewMetrics(false)
	d.Metrics.ObserveRequest("GET", "/devices/{id}", 200, 120*time.Millisecond)
	d.Metrics.ObserveRequest("GET", "/devices/{id}", 0, time.Second)
	mux := newMux(d)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, want := range []string{
		`comfortd_up{unit="office"} 1`,
		`comfortd_mode{mode="`,
		`comfortd_setpoint_celsius{kind="heat",unit="office"}`,
		`comfortd_room_temperature_celsius{unit="house"}`,
		`comfortd_alert{alert="hot_adjust",unit="office"}`,
		`comfortd_status_display{unit="office"}`,
		`comfortd_upstream_requests_total{code="200",method="GET",route="/devices/{id}"} 1`,
		`comfortd_upstream_requests_total{code="0",method="GET",route="/devices/{id}"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s\n%s", want, body)
		}
	}
	if strings.Contains(body, "go_goroutines") {
		t.Error("registry should be private")
	}
}

func TestSocketMetrics(t *testing.T) {
	_, d := ctlDeps(t)
	d.Metrics = NewMetrics(false)
	up := true
	d.Metrics.RegisterSocket(func() bool { return up }, func() int { return 3 }, func() int { return 2 })
	d.Metrics.SocketEvent("hold_update")
	d.Metrics.SocketEvent("hold_update")
	d.Metrics.SocketMissed("spHeat")
	mux := newMux(d)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{
		"comfortd_socket_connected 1",
		"comfortd_socket_reconnects_total 3",
		"comfortd_socket_dropped_total 2",
		`comfortd_socket_events_total{event="hold_update"} 2`,
		`comfortd_socket_missed_changes_total{field="spHeat"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s\n%s", want, body)
		}
	}
	up = false
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), "comfortd_socket_connected 0") {
		t.Error("connected should drop to 0")
	}
}
