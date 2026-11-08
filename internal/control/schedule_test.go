// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"errors"
	"testing"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

func ev(at, mode string, h, c float64, days ...string) kumo.ScheduleEvent {
	return kumo.ScheduleEvent{Days: days, StartTime: at, Mode: mode, SpHeat: &h, SpCool: &c}
}

func TestValidateSchedule(t *testing.T) {
	ok, err := ValidateSchedule([]kumo.ScheduleEvent{ev("0700", "heat", 20, 26, "Mo", "Su"), ev("0700", "cool", 20, 24, "Tu")})
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
		if _, err := ValidateSchedule(evs); !errors.Is(err, ErrInvalid) {
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
