// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"encoding/json"
	"testing"
	"time"
)

func TestScheduleDays(t *testing.T) {
	for in, want := range map[string]string{
		"Su Mo Tu We Th Fr Sa": "Every day",
		"Mo Tu We Th Fr":       "Mon–Fri",
		"Sa Su":                "Sat, Sun",
		"We Mo":                "Mon, Wed",
	} {
		var days []string
		for _, d := range splitFields(in) {
			days = append(days, d)
		}
		if got := scheduleDays(days); got != want {
			t.Errorf("%s: %q want %q", in, got, want)
		}
	}
}

func splitFields(s string) []string {
	var out []string
	for i := 0; i+2 <= len(s); i += 3 {
		out = append(out, s[i:i+2])
	}
	return out
}

func TestBuildScheduleNextAndRows(t *testing.T) {
	sched := json.RawMessage(`{"season":"Winter","events":[
	 {"days":["Su","Mo","Tu","We","Th","Fr","Sa"],"startTime":"2200","operationMode":"heat","fanSpeed":null,"spHeat":19,"spCool":26},
	 {"days":["Su","Mo","Tu","We","Th","Fr","Sa"],"startTime":"0200","operationMode":"off","fanSpeed":null,"spHeat":21,"spCool":26},
	 {"days":["Mo","Tu","We","Th","Fr"],"startTime":"0730","operationMode":"heat","fanSpeed":"quiet","spHeat":20,"spCool":26}]}`)
	sn := fixtureSnap(t, "office", "device_office.json", map[string]json.RawMessage{"schedule": sched})
	la, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 11, 7, 20, 40, 0, 0, la) // Saturday evening
	v := BuildSchedule(sn, now)
	if !v.Present || v.Season != "Winter" || len(v.Rows) != 3 {
		t.Fatalf("%+v", v)
	}
	if v.Next != "today 22:00 → Heat 19 °C" {
		t.Errorf("next %q", v.Next)
	}
	// Rows are in clock order; the 22:00 row is the next one.
	if v.Rows[0].At != "02:00" || v.Rows[0].Mode != "Off" || v.Rows[0].Target != "–" {
		t.Errorf("row 0 %+v", v.Rows[0])
	}
	if v.Rows[1].Fan != "Low" || v.Rows[1].Days != "Mon–Fri" {
		t.Errorf("row 1 %+v", v.Rows[1])
	}
	for i, r := range v.Rows {
		if r.IsNext != (r.At == "22:00") {
			t.Errorf("row %d IsNext=%v", i, r.IsNext)
		}
	}
	// After the 22:00 event the next one is the 02:00 off, tomorrow, with no target.
	v = BuildSchedule(sn, time.Date(2026, 11, 7, 22, 30, 0, 0, la))
	if v.Next != "tomorrow 02:00 → Off" {
		t.Errorf("next %q", v.Next)
	}
}

func TestBuildScheduleMissing(t *testing.T) {
	sn := fixtureSnap(t, "office", "device_office.json", nil)
	if v := BuildSchedule(sn, time.Now()); v.Present || v.Error != "" {
		t.Errorf("%+v", v)
	}
	sn.Errors["schedule"] = "not found"
	if v := BuildSchedule(sn, time.Now()); v.Present || v.Error != "not found" {
		t.Errorf("%+v", v)
	}
}
