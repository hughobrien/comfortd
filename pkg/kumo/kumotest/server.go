// SPDX-License-Identifier: GPL-3.0-or-later

// Package kumotest is a fake Kumo Cloud server built on redacted real
// responses. Tests and the daemon's -fake mode use it; nothing here talks to a
// real air conditioner.
package kumotest

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

//go:embed fixtures/*.json
var fixtures embed.FS

const (
	User         = "user@example.com"
	Password     = "test-password"
	OfficeSerial = "TESTSERIAL0001"
	HouseSerial  = "TESTSERIAL0002"
	SiteID       = "00000000-0000-4000-8000-000000000001"
	HouseZoneID  = "00000000-0000-4000-8000-000000000004"
	OfficeZoneID = "00000000-0000-4000-8000-000000000007"
)

// Command is a recorded send-command request.
type Command struct {
	Serial   string
	Commands map[string]any
}

type failure struct {
	status, n int
}

// Server is the fake. URL is the base, equivalent to .../v3.
type Server struct {
	URL string

	srv *httptest.Server
	mu  sync.Mutex

	devices       map[string]map[string]any
	logins        int
	refreshes     int
	tokenSeq      int
	access        string
	refresh       string
	expired       map[string]bool
	refreshNested bool
	commands      []Command
	fails         map[string]*failure
	requests      map[string]int
	schedules     []map[string]any // the winter season's per-zone schedules, loaded on first use
	scheduleSeq   int
	schedWrites   []ScheduleWrite
	holds         map[string]map[string]any // zone id -> holdMode set by a hold write
	holdWrites    []HoldWrite
}

// ScheduleWrite is a recorded schedule change: "add" with the event's fields,
// or "delete" with the event's id in Event["id"].
type ScheduleWrite struct {
	Op    string
	Event map[string]any
}

func fixture(name string) []byte {
	b, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

func fixtureMap(name string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(fixture(name), &m); err != nil {
		panic(err)
	}
	return m
}

// New starts a fake server.
func New() *Server {
	s := &Server{
		devices: map[string]map[string]any{
			OfficeSerial: fixtureMap("device_office.json"),
			HouseSerial:  fixtureMap("device_house.json"),
		},
		holds:    map[string]map[string]any{},
		expired:  map[string]bool{},
		fails:    map[string]*failure{},
		requests: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /refresh", s.handleRefresh)
	mux.HandleFunc("GET /sites/", s.authed(s.handleSites))
	mux.HandleFunc("GET /sites/{site}/zones", s.authed(s.handleZones))
	mux.HandleFunc("GET /devices/{serial}", s.authed(s.handleDevice))
	mux.HandleFunc("GET /devices/{serial}/profile", s.authed(s.handleFixtureBySerial("profile_%s.json", 200)))
	mux.HandleFunc("GET /devices/{serial}/status", s.authed(s.handleFixtureBySerial("status_%s.json", 200)))
	mux.HandleFunc("GET /devices/{serial}/sensor", s.authed(s.handleFixtureBySerial("sensor_%s_404.json", 404)))
	mux.HandleFunc("GET /devices/{serial}/mhk2", s.authed(s.handleMhk2))
	mux.HandleFunc("GET /v4/sites/{site}/schedule-seasons", s.authed(s.handleFixture("schedule_seasons.json")))
	mux.HandleFunc("GET /v4/schedule-seasons/{season}/schedules", s.authed(s.handleSeasonSchedules))
	mux.HandleFunc("POST /v4/schedule-seasons/{season}/schedules", s.authed(s.handleAddEvents))
	mux.HandleFunc("DELETE /v4/schedules/{schedule}/events", s.authed(s.handleDeleteEvents))
	mux.HandleFunc("POST /v4/sites/{site}/hold", s.authed(s.handleHold))
	mux.HandleFunc("POST /devices/send-command", s.authed(s.handleSendCommand))
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.URL.Path]++
		f := s.fails[r.URL.Path]
		fail := 0
		if f != nil && f.n > 0 {
			f.n--
			fail = f.status
		}
		s.mu.Unlock()
		if fail != 0 {
			writeJSON(w, fail, []byte("{}"))
			return
		}
		mux.ServeHTTP(w, r)
	}))
	s.URL = s.srv.URL
	return s
}

func (s *Server) Close() { s.srv.Close() }

func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *Server) Refreshes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes
}

func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Command(nil), s.commands...)
}

// FailNext makes the next n requests to path return status with body {}.
func (s *Server) FailNext(path string, status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails[path] = &failure{status: status, n: n}
}

// ExpireAccess makes every access token issued so far return 401.
func (s *Server) ExpireAccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired[s.access] = true
}

// SetField simulates the app or a schedule changing the unit.
func (s *Server) SetField(serial, field string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[serial][field] = v
}

// Device returns a copy of the live device map.
func (s *Server) Device(serial string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyMap(s.devices[serial])
}

func (s *Server) Requests(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[path]
}

func (s *Server) SetRefreshNested(nested bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshNested = nested
}

func copyMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func writeValue(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	writeJSON(w, status, b)
}

// newTokens must be called with s.mu held.
func (s *Server) newTokens() (access, refresh string) {
	s.tokenSeq++
	s.access = fmt.Sprintf("access-%d", s.tokenSeq)
	s.refresh = fmt.Sprintf("refresh-%d", s.tokenSeq)
	return s.access, s.refresh
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.logins++
	if body.Username != User || body.Password != Password {
		s.mu.Unlock()
		writeJSON(w, 403, []byte(`{"error":"forbidden"}`))
		return
	}
	a, rf := s.newTokens()
	s.mu.Unlock()
	out := fixtureMap("login.json")
	out["token"] = map[string]any{"access": a, "refresh": rf}
	writeValue(w, 200, out)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct{ Refresh string }
	json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	if body.Refresh == "" || body.Refresh != s.refresh {
		s.mu.Unlock()
		writeJSON(w, 401, []byte(`{"error":"unauthorized"}`))
		return
	}
	s.refreshes++
	a, rf := s.newTokens()
	nested := s.refreshNested
	s.mu.Unlock()
	tok := map[string]any{"access": a, "refresh": rf}
	if nested {
		writeValue(w, 200, map[string]any{"token": tok})
		return
	}
	writeValue(w, 200, tok)
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-app-version") == "" {
			writeJSON(w, 400, []byte(`{"error":"missing x-app-version"}`))
			return
		}
		s.mu.Lock()
		want := "Bearer " + s.access
		ok := s.access != "" && r.Header.Get("Authorization") == want && !s.expired[s.access]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, 401, []byte(`{"error":"unauthorized"}`))
			return
		}
		h(w, r)
	}
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, fixture("sites.json"))
}

func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("site") != SiteID {
		writeJSON(w, 404, []byte(`{}`))
		return
	}
	var zones []map[string]any
	json.Unmarshal(fixture("zones.json"), &zones)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, z := range zones {
		if h, ok := s.holds[z["id"].(string)]; ok {
			z["holdMode"] = h
		}
		ad, _ := z["adapter"].(map[string]any)
		serial, _ := ad["deviceSerial"].(string)
		dev, ok := s.devices[serial]
		if !ok {
			continue
		}
		for _, k := range []string{"operationMode", "power", "spHeat", "spCool", "roomTemp"} {
			if v, ok := dev[k]; ok {
				ad[k] = v
			}
		}
	}
	writeValue(w, 200, zones)
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	dev, ok := s.devices[r.PathValue("serial")]
	var b []byte
	if ok {
		b, _ = json.Marshal(dev)
	}
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 404, []byte(`{}`))
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) handleFixtureBySerial(pattern string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var which string
		switch r.PathValue("serial") {
		case OfficeSerial:
			which = "office"
		case HouseSerial:
			which = "house"
		default:
			writeJSON(w, 404, []byte(`{}`))
			return
		}
		writeJSON(w, status, fixture(fmt.Sprintf(pattern, which)))
	}
}

// handleMhk2 answers like the cloud: House has a wall thermostat, Office 404s.
func (s *Server) handleMhk2(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("serial") == HouseSerial {
		writeJSON(w, 200, fixture("mhk2_house.json"))
		return
	}
	writeJSON(w, 404, []byte(`{}`))
}

func (s *Server) handleSendCommand(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		DeviceSerial string         `json:"deviceSerial"`
		Commands     map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, 400, []byte(`{}`))
		return
	}
	s.mu.Lock()
	dev, ok := s.devices[body.DeviceSerial]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, 404, []byte(`{}`))
		return
	}
	s.commands = append(s.commands, Command{Serial: body.DeviceSerial, Commands: body.Commands})
	for k, v := range body.Commands {
		dev[k] = v
		if k == "operationMode" {
			if v == "off" {
				dev["power"] = float64(0)
			} else {
				dev["power"] = float64(1)
			}
		}
	}
	s.mu.Unlock()
	writeValue(w, 200, map[string]any{"devices": []string{body.DeviceSerial}})
}

func (s *Server) handleFixture(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, fixture(name)) }
}

// WinterSeasonID is the running season in the fake; any other season has no schedules.
const WinterSeasonID = "00000000-0000-4000-8000-0000000000a1"

// loadSchedules must be called with s.mu held.
func (s *Server) loadSchedules() {
	if s.schedules == nil {
		if err := json.Unmarshal(fixture("schedule_winter.json"), &s.schedules); err != nil {
			panic(err)
		}
	}
}

func (s *Server) handleSeasonSchedules(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("season") != WinterSeasonID {
		writeJSON(w, 200, []byte("[]"))
		return
	}
	s.mu.Lock()
	s.loadSchedules()
	b, _ := json.Marshal(s.schedules)
	s.mu.Unlock()
	writeJSON(w, 200, b)
}

// ScheduleWrites returns the schedule changes made so far.
func (s *Server) ScheduleWrites() []ScheduleWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ScheduleWrite(nil), s.schedWrites...)
}

func (s *Server) handleAddEvents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Schedules []struct {
			Zone   string           `json:"zone"`
			Events []map[string]any `json:"events"`
		} `json:"schedules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.PathValue("season") != WinterSeasonID {
		writeJSON(w, 400, []byte(`{}`))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadSchedules()
	for _, in := range body.Schedules {
		for _, sc := range s.schedules {
			if zone, _ := sc["zone"].(map[string]any); zone["id"] != in.Zone {
				continue
			}
			evs, _ := sc["events"].([]any)
			for _, e := range in.Events {
				s.scheduleSeq++
				e["id"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", 900000+s.scheduleSeq)
				evs = append(evs, e)
				s.schedWrites = append(s.schedWrites, ScheduleWrite{"add", e})
			}
			sc["events"] = evs
		}
	}
	writeJSON(w, 201, []byte(`{}`))
}

func (s *Server) handleDeleteEvents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, []byte(`{}`))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadSchedules()
	for _, sc := range s.schedules {
		if sc["id"] != r.PathValue("schedule") {
			continue
		}
		evs, _ := sc["events"].([]any)
		var keep []any
		for _, e := range evs {
			m, _ := e.(map[string]any)
			del := false
			for _, id := range body.Events {
				del = del || m["id"] == id
			}
			if del {
				s.schedWrites = append(s.schedWrites, ScheduleWrite{"delete", m})
			} else {
				keep = append(keep, e)
			}
		}
		sc["events"] = keep
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 404, []byte(`{}`))
}

// HoldWrite is an accepted hold change. EndTime is nil for a permanent hold
// or an end; Enabled is false for an end.
type HoldWrite struct {
	Zone      string
	Enabled   bool
	EndTime   *int64
	Permanent bool
}

// HoldWrites returns the accepted hold changes so far.
func (s *Server) HoldWrites() []HoldWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HoldWrite(nil), s.holdWrites...)
}

// handleHold mimics POST /v4/sites/{id}/hold: endTime must be a number or
// null; the reply carries the zone's new holdMode.
func (s *Server) handleHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SiteID  string          `json:"siteId"`
		EndTime json.RawMessage `json:"endTime"`
		Zones   []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"zones"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.PathValue("site") != SiteID || len(body.Zones) == 0 {
		writeJSON(w, 400, []byte(`{}`))
		return
	}
	var end *int64
	if len(body.EndTime) > 0 && string(body.EndTime) != "null" {
		var n int64
		if json.Unmarshal(body.EndTime, &n) != nil {
			writeJSON(w, 400, []byte(`{"error":{"endTime":"Expected number, received string"}}`))
			return
		}
		end = &n
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var modes []map[string]any
	for _, z := range body.Zones {
		hm := map[string]any{"id": "00000000-0000-4000-8000-0000000000a1", "enabled": z.Enabled, "type": "hold", "origin": "adapter",
			"operationMode": nil, "fanSpeed": nil, "airDirection": nil, "spCool": nil, "spHeat": nil}
		stamp := func(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }
		switch {
		case !z.Enabled:
			hm["holdType"], hm["endTime"] = "permanent", stamp(now)
		case end != nil:
			hm["holdType"], hm["endTime"] = "until_specific_time", stamp(time.Unix(*end, 0).UTC())
		default:
			hm["holdType"], hm["endTime"] = "permanent", stamp(now.AddDate(100, 0, 0))
		}
		s.holds[z.ID] = hm
		modes = append(modes, hm)
		s.holdWrites = append(s.holdWrites, HoldWrite{Zone: z.ID, Enabled: z.Enabled, EndTime: end, Permanent: z.Enabled && end == nil})
	}
	out := map[string]any{"holdModes": modes}
	if z := body.Zones[0]; z.Enabled {
		if end != nil {
			out["endTime"] = *end
		} else {
			out["endTime"] = nil
		}
	}
	writeValue(w, 201, out)
}
