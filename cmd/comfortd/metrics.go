// SPDX-License-Identifier: GPL-3.0-or-later

// Prometheus metrics, in the style of breezyd: a private registry (no Go
// runtime or library collectors), per-unit gauges rebuilt from the store
// before every scrape, and counters for what we ask the cloud. Gauges that
// have no value right now are left out rather than held at an old number;
// comfortd_up and comfortd_last_poll_timestamp_seconds say how fresh the rest is.
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns every collector exposed at /metrics.
type Metrics struct {
	reg *prometheus.Registry

	up, lastPoll, connected, power, room, setpoint, humidity, rssi *prometheus.GaugeVec
	mode, fan                                                      *prometheus.GaugeVec // one-hot
	hold, scheduleActive, alert, thermostatBattery, endpointError  *prometheus.GaugeVec

	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	// perUnit are reset on every Update so a value that went away disappears.
	perUnit []*prometheus.GaugeVec
}

func NewMetrics() *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	g := func(name, help string, labels ...string) *prometheus.GaugeVec {
		v := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, append([]string{"unit"}, labels...))
		m.reg.MustRegister(v)
		m.perUnit = append(m.perUnit, v)
		return v
	}
	m.up = g("comfortd_up", "1 if the unit's last device poll is fresh and the unit is online, else 0.")
	m.lastPoll = g("comfortd_last_poll_timestamp_seconds", "Unix time of the last successful device poll.")
	m.connected = g("comfortd_connected", "The cloud says the unit's adapter is connected (0/1).")
	m.power = g("comfortd_power", "Unit power (0/1).")
	m.mode = g("comfortd_mode", "Operation mode, one series per mode with value 1 for the current one.", "mode")
	m.fan = g("comfortd_fan_speed", "Fan speed, one series per speed name with value 1 for the current one.", "speed")
	m.room = g("comfortd_room_temperature_celsius", "Room temperature the unit reports.")
	m.setpoint = g("comfortd_setpoint_celsius", "Heat and cool setpoints.", "kind")
	m.humidity = g("comfortd_humidity_percent", "Relative humidity, for units that report it.")
	m.rssi = g("comfortd_wifi_rssi_dbm", "Adapter Wi-Fi signal strength.")
	m.hold = g("comfortd_hold_active", "The Comfort app's temporary hold is on (0/1).")
	m.scheduleActive = g("comfortd_schedule_active", "The unit follows its schedule (0/1).")
	m.alert = g("comfortd_alert", "Unit indicator flags (filter, defrost, standby), 0/1.", "alert")
	m.thermostatBattery = g("comfortd_thermostat_battery_ok", "Wall thermostat battery is ok (0/1), for zones with an MHK2.")
	m.endpointError = g("comfortd_endpoint_error", "1 for each endpoint whose last poll failed.", "endpoint")

	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "comfortd_upstream_requests_total",
		Help: "Requests to the Kumo cloud by method, route and status (0 = failed before a response).",
	}, []string{"method", "route", "code"})
	m.latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "comfortd_upstream_request_duration_seconds",
		Help:    "Time taken by requests to the Kumo cloud, by route.",
		Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 15},
	}, []string{"route"})
	m.reg.MustRegister(m.requests, m.latency)
	return m
}

// ObserveRequest is the kumo client's OnRequest hook.
func (m *Metrics) ObserveRequest(method, route string, status int, took time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.latency.WithLabelValues(route).Observe(took.Seconds())
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Update rebuilds the per-unit gauges from the store.
func (m *Metrics) Update(st *store.Store, now time.Time, interval time.Duration) {
	for _, v := range m.perUnit {
		v.Reset()
	}
	for _, s := range st.All() {
		u := s.Unit.Slug
		for ep := range s.Errors {
			if s.Errors[ep] != "" {
				m.endpointError.WithLabelValues(u, ep).Set(1)
			}
		}
		m.up.WithLabelValues(u).Set(b2f(s.Device != nil && !s.Stale(now, interval)))
		if !s.LastDeviceOK.IsZero() {
			m.lastPoll.WithLabelValues(u).Set(float64(s.LastDeviceOK.Unix()))
		}
		if d := s.Device; d != nil {
			m.connected.WithLabelValues(u).Set(b2f(d.Connected))
			m.power.WithLabelValues(u).Set(float64(d.Power))
			if d.OperationMode != "" {
				m.mode.WithLabelValues(u, d.OperationMode).Set(1)
			}
			if d.FanSpeed != "" {
				m.fan.WithLabelValues(u, d.FanSpeed).Set(1)
			}
			if d.RoomTemp != nil {
				m.room.WithLabelValues(u).Set(*d.RoomTemp)
			}
			if d.SpHeat != nil {
				m.setpoint.WithLabelValues(u, "heat").Set(*d.SpHeat)
			}
			if d.SpCool != nil {
				m.setpoint.WithLabelValues(u, "cool").Set(*d.SpCool)
			}
			if d.Humidity != nil {
				m.humidity.WithLabelValues(u).Set(*d.Humidity)
			}
			m.alert.WithLabelValues(u, "filter").Set(b2f(d.Display.Filter))
			m.alert.WithLabelValues(u, "defrost").Set(b2f(d.Display.Defrost))
			m.alert.WithLabelValues(u, "standby").Set(b2f(d.Display.Standby))
		}
		var dev struct {
			Rssi *float64 `json:"rssi"`
		}
		if json.Unmarshal(s.Raw[store.EndpointDevice], &dev) == nil && dev.Rssi != nil {
			m.rssi.WithLabelValues(u).Set(*dev.Rssi)
		}
		if z := s.Zone; z != nil {
			m.hold.WithLabelValues(u).Set(b2f(z.HoldEnabled))
			m.scheduleActive.WithLabelValues(u).Set(b2f(z.HasActiveSchedule))
		}
		var th struct {
			Battery string `json:"thermostatBattery"`
		}
		if json.Unmarshal(s.Raw[store.EndpointMhk2], &th) == nil && th.Battery != "" {
			m.thermostatBattery.WithLabelValues(u).Set(b2f(th.Battery == "ok"))
		}
	}
}

// Handler serves /metrics, refreshing the gauges first.
func (m *Metrics) Handler(st *store.Store, now func() time.Time, interval func() time.Duration) http.Handler {
	h := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Update(st, now(), interval())
		h.ServeHTTP(w, r)
	})
}
