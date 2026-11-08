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
	d.Metrics = NewMetrics()
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
