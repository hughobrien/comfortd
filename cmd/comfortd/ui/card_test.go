// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/annotate"
	"github.com/hughobrien/comfortd/internal/store"
)

func fixtureSnap(t *testing.T, slug, file string, extra map[string]json.RawMessage) store.Snapshot {
	t.Helper()
	raw, err := os.ReadFile("../../../pkg/kumo/kumotest/fixtures/" + file)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New([]store.Unit{{Slug: slug, Name: slug, Serial: "S"}})
	now := time.Now()
	if err := st.Put(slug, "device", raw, now); err != nil {
		t.Fatal(err)
	}
	for ep, r := range extra {
		if err := st.Put(slug, ep, r, now); err != nil {
			t.Fatal(err)
		}
	}
	sn, _ := st.Get(slug)
	return sn
}

func mustAnn(t *testing.T) *annotate.Set {
	t.Helper()
	a, err := annotate.Load()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRowsAnnotated(t *testing.T) {
	a := mustAnn(t)
	v := BuildUnitView(fixtureSnap(t, "office", "device_office.json", nil), nil, time.Now(), time.Minute, a, CardControl{})
	r, ok := findRow(v, "device", "operationMode")
	f, _ := a.Lookup("device", "operationMode")
	if !ok || r.Label != "Mode" || r.Decoded != "Heat" || r.Confidence != f.Confidence {
		t.Fatalf("%+v", r)
	}
}

func TestUnknownFields(t *testing.T) {
	zone := json.RawMessage(`{"id":"z","name":"Office","hasActiveSchedule":true,"holdMode":{"enabled":false},"tags":["a"],"adapter":{"deviceSerial":"TESTSERIAL0001"}}`)
	v := BuildUnitView(fixtureSnap(t, "office", "device_office.json", map[string]json.RawMessage{"zone": zone}), nil, time.Now(), time.Minute, mustAnn(t), CardControl{})
	has := map[string]bool{}
	for _, r := range v.Unknown {
		has[r.Path] = true
	}
	if !has["twoFiguresCode"] || !has["tags[0]"] {
		t.Errorf("missing expected unknowns: %v", has)
	}
	if has["updatedAt"] || has["operationMode"] || has["hasActiveSchedule"] {
		t.Errorf("unexpected unknowns: %v", has)
	}
}

func TestCardFromFixture(t *testing.T) {
	zone := json.RawMessage(`{"id":"z","name":"Office","hasActiveSchedule":true,"holdMode":{"enabled":false},"adapter":{"deviceSerial":"TESTSERIAL0001"}}`)
	c := BuildCard(fixtureSnap(t, "office", "device_office.json", map[string]json.RawMessage{"zone": zone}), mustAnn(t), CardControl{}, time.Now(), time.Minute)
	if c.Mode != "Heat" || c.SpHeat != "19" || c.RoomTemp != "20.5" || c.Humidity != dash || !c.ScheduleActive {
		t.Fatalf("%+v", c)
	}
	h := BuildCard(fixtureSnap(t, "house", "device_house.json", nil), mustAnn(t), CardControl{}, time.Now(), time.Minute)
	if h.Humidity != "52" {
		t.Fatalf("house humidity %q", h.Humidity)
	}
}

func TestCardMissingValues(t *testing.T) {
	raw := json.RawMessage(`{"deviceSerial":"X","connected":true,"operationMode":"heat","power":1}`)
	st := store.New([]store.Unit{{Slug: "u"}})
	st.Put("u", "device", raw, time.Now())
	sn, _ := st.Get("u")
	c := BuildCard(sn, nil, CardControl{}, time.Now(), time.Minute)
	if c.SpHeat != dash || c.RoomTemp != dash || c.Mode != "heat" {
		t.Fatalf("%+v", c)
	}
	// no data at all must not panic
	_ = BuildCard(store.Snapshot{}, nil, CardControl{}, time.Now(), time.Minute)
}

func TestCardStale(t *testing.T) {
	sn := fixtureSnap(t, "office", "device_office.json", nil)
	now := time.Now()
	sn.LastDeviceOK = now.Add(-4 * time.Minute)
	c := BuildCard(sn, nil, CardControl{}, now, time.Minute)
	if !c.Stale || c.LastOK == "never" {
		t.Fatalf("%+v", c)
	}
}

func TestUndecodedValue(t *testing.T) {
	raw := json.RawMessage(`{"deviceSerial":"X","connected":true,"operationMode":"cool","power":1}`)
	st := store.New([]store.Unit{{Slug: "u"}})
	st.Put("u", "device", raw, time.Now())
	sn, _ := st.Get("u")
	v := BuildUnitView(sn, nil, time.Now(), time.Minute, mustAnn(t), CardControl{})
	if r, _ := findRow(v, "device", "operationMode"); r.Decoded != "(undecoded)" {
		t.Fatalf("%+v", r)
	}
}

func TestCardAlertsImageAndRange(t *testing.T) {
	status := json.RawMessage(`{"minSetPoint":16,"maxSetPoint":31}`)
	sn := fixtureSnap(t, "office", "device_office.json", map[string]json.RawMessage{"status": status})
	c := BuildCard(sn, mustAnn(t), CardControl{Writable: true}, time.Now(), time.Minute)
	if len(c.Alerts) != 1 || c.Alerts[0] != "check filter" { // fixture has filter:true only
		t.Fatalf("alerts %v", c.Alerts)
	}
	if c.Image == "" {
		t.Fatal("no image")
	}
	if c.SpMin != "16" || c.SpMax != "31" {
		t.Fatalf("range %q–%q (want the status endpoint's, not the profile's 10–31)", c.SpMin, c.SpMax)
	}
}

func TestCardModesAndSetpointsFollowMode(t *testing.T) {
	prof, err := os.ReadFile("../../../pkg/kumo/kumotest/fixtures/profile_office.json")
	if err != nil {
		t.Fatal(err)
	}
	status := json.RawMessage(`{"minSetPoint":16,"maxSetPoint":31}`)
	extra := map[string]json.RawMessage{"profile": prof, "status": status}
	sn := fixtureSnap(t, "office", "device_office.json", extra)
	var vals []string
	for _, m := range BuildCard(sn, mustAnn(t), CardControl{Writable: true}, time.Now(), time.Minute).Modes {
		vals = append(vals, m.Value)
	}
	if got := strings.Join(vals, ","); got != "heat,cool,dry,auto,vent" {
		t.Fatalf("modes %s", got)
	}
	cases := map[string][2]bool{"heat": {true, false}, "cool": {false, true}, "autoCool": {true, true}, "autoHeat": {true, true}, "dry": {false, false}, "vent": {false, false}}
	for mode, want := range cases {
		s := sn
		d := *sn.Device
		d.OperationMode = mode
		s.Device = &d
		c := BuildCard(s, mustAnn(t), CardControl{Writable: true}, time.Now(), time.Minute)
		if c.ShowHeat != want[0] || c.ShowCool != want[1] {
			t.Errorf("%s: heat=%v cool=%v", mode, c.ShowHeat, c.ShowCool)
		}
		if (mode == "autoCool" || mode == "autoHeat") && !pressed(c.Modes, "auto") {
			t.Errorf("%s should light the Auto button", mode)
		}
	}
}

func pressed(cs []Choice, v string) bool {
	for _, c := range cs {
		if c.Value == v && c.Pressed {
			return true
		}
	}
	return false
}
