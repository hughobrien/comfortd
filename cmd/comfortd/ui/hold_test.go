// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

func TestBuildHold(t *testing.T) {
	sched := json.RawMessage(`{"season":"Winter","events":[
	 {"days":["Su","Mo","Tu","We","Th","Fr","Sa"],"startTime":"2200","operationMode":"heat","fanSpeed":null,"spHeat":19,"spCool":26},
	 {"days":["Su","Mo","Tu","We","Th","Fr","Sa"],"startTime":"0200","operationMode":"off","fanSpeed":null,"spHeat":21,"spCool":26}]}`)
	la, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 11, 7, 20, 40, 0, 0, la)
	at2200 := time.Date(2026, 11, 7, 22, 0, 0, 0, la)
	far := time.Date(2126, 11, 17, 2, 56, 41, 0, time.UTC)
	cases := []struct {
		name     string
		z        *kumo.Zone
		writable bool
		mode     string
		time     string
	}{
		{"no hold", &kumo.Zone{}, true, "none", ""},
		{"app until next event", &kumo.Zone{HoldEnabled: true, HoldType: "until_next_event", HoldEnd: at2200}, true, "next", ""},
		{"ours until the next event", &kumo.Zone{HoldEnabled: true, HoldType: "until_specific_time", HoldEnd: at2200}, true, "next", ""},
		{"until another time", &kumo.Zone{HoldEnabled: true, HoldType: "until_specific_time", HoldEnd: at2200.Add(30 * time.Minute)}, true, "until", "22:30"},
		{"permanent hides its date", &kumo.Zone{HoldEnabled: true, HoldType: "permanent", HoldEnd: far}, true, "permanent", ""},
		{"unknown type", &kumo.Zone{HoldEnabled: true, HoldType: "unset", HoldEnd: at2200.Add(time.Hour)}, true, "until", "23:00"},
	}
	for _, c := range cases {
		sn := fixtureSnap(t, "office", "device_office.json", map[string]json.RawMessage{"schedule": sched})
		sn.Zone = c.z
		h := BuildHold(sn, c.writable, now)
		if !h.Show || h.Mode != c.mode || h.Time != c.time || !h.CanNext || !h.NextAt.Equal(at2200) {
			t.Errorf("%s: %+v", c.name, h)
		}
	}
	sn := fixtureSnap(t, "office", "device_office.json", nil)
	sn.Zone = &kumo.Zone{}
	if h := BuildHold(sn, true, now); !h.Show || h.CanNext || h.Mode != "none" {
		t.Errorf("no schedule: %+v", h)
	}
	if h := BuildHold(sn, false, now); h.Show {
		t.Errorf("read-only unit: %+v", h)
	}
	sn.Zone = nil
	if h := BuildHold(sn, true, now); h.Show {
		t.Errorf("no zone: %+v", h)
	}
}

func TestHoldTimeFromClock(t *testing.T) {
	sn := fixtureSnap(t, "office", "device_office.json", nil)
	la, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 11, 7, 20, 40, 0, 0, la)
	for in, want := range map[string]string{"22:30": "2026-11-07 22:30", "20:40": "2026-11-08 20:40", "08:00": "2026-11-08 08:00"} {
		got, err := HoldTimeFromClock(sn, now, in)
		if err != nil || got.In(la).Format("2006-01-02 15:04") != want {
			t.Errorf("%s: %v %v want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "25:99", "2230", "ab:cd", "22:3"} {
		if _, err := HoldTimeFromClock(sn, now, bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
