// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"errors"
	"testing"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

func ev(at, mode string, h, c float64, days ...string) kumo.ScheduleEvent {
	return kumo.ScheduleEvent{Days: days, StartTime: at, Mode: mode, SpHeat: &h, SpCool: &c}
}

func TestValidateSchedule(t *testing.T) {
	lim := ScheduleLimitsFor(store.Snapshot{})
	ok, err := ValidateSchedule([]kumo.ScheduleEvent{ev("0700", "heat", 20, 26, "Mo", "Su"), ev("0700", "cool", 20, 24, "Tu")}, lim)
	if err != nil || ok[0].Fan != "auto" || ok[0].Vane != "auto" || ok[0].Days[0] != "Su" {
		t.Fatalf("%+v %v", ok, err)
	}
	bad := map[string][]kumo.ScheduleEvent{
		"time":   {ev("2460", "heat", 20, 26, "Mo")},
		"mode":   {ev("0700", "boil", 20, 26, "Mo")},
		"days":   {ev("0700", "heat", 20, 26)},
		"day":    {ev("0700", "heat", 20, 26, "Xx")},
		"range":  {ev("0700", "heat", 9, 26, "Mo")},
		"step":   {ev("0700", "heat", 20.3, 26, "Mo")},
		"gap":    {ev("0700", "auto", 22, 23, "Mo")},
		"clash":  {ev("0700", "heat", 20, 26, "Mo", "Tu"), ev("0700", "off", 20, 26, "Tu")},
		"nosets": {{Days: []string{"Mo"}, StartTime: "0700", Mode: "off"}},
	}
	for name, evs := range bad {
		if _, err := ValidateSchedule(evs, lim); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestSameEventIgnoresOrderAndDefaults(t *testing.T) {
	a, b := ev("0700", "heat", 20, 26, "Mo", "Tu"), ev("0700", "heat", 20, 26, "Tu", "Mo")
	b.Fan = "auto"
	if !sameEvent(a, b) {
		t.Fatal("should match")
	}
	b.Mode = "cool"
	if sameEvent(a, b) {
		t.Fatal("should differ")
	}
}

func TestScheduleLimitsFollowTheUnit(t *testing.T) {
	// A 3-speed, heat/cool-only unit with a 19.5–28 status range, like House.
	lim := ScheduleLimits{
		Modes: []Choice{{"heat", "Heat"}, {"cool", "Cool"}, {"off", "Off"}},
		Fans:  FanChoices(3),
		Lo:    19.5, Hi: 28,
	}
	hot := ev("0700", "heat", 20, 26, "Mo")
	if _, err := ValidateSchedule([]kumo.ScheduleEvent{hot}, lim); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]kumo.ScheduleEvent{
		"fan":  func() kumo.ScheduleEvent { e := hot; e.Fan = "superQuiet"; return e }(),
		"mode": ev("0700", "dry", 20, 26, "Mo"),
		"low":  ev("0700", "heat", 19, 26, "Mo"),
		"high": ev("0700", "heat", 20, 29, "Mo"),
	} {
		if _, err := ValidateSchedule([]kumo.ScheduleEvent{e}, lim); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
