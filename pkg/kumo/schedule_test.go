// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func TestScheduleNext(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	s := Schedule{Events: []ScheduleEvent{
		{Days: []string{"Mo", "Tu", "We", "Th", "Fr"}, StartTime: "0730", Mode: "heat", SpHeat: f(20)},
		{Days: []string{"Sa", "Su"}, StartTime: "1000", Mode: "heat", SpHeat: f(20)},
		{Days: dayCodes[:], StartTime: "2200", Mode: "heat", SpHeat: f(19)},
		{Days: dayCodes[:], StartTime: "0200", Mode: "off"},
	}}
	cases := []struct {
		now, want string
		mode      string
	}{
		{"2026-11-07 20:40", "2026-11-07 22:00", "heat"}, // Sat evening: the 22:00 event
		{"2026-11-07 22:00", "2026-11-08 02:00", "off"},  // strictly after
		{"2026-11-08 02:30", "2026-11-08 10:00", "heat"}, // Sun: weekend morning
		{"2026-11-08 23:30", "2026-11-09 02:00", "off"},  // wraps midnight
		{"2026-11-09 03:00", "2026-11-09 07:30", "heat"}, // Mon: weekday morning
		{"2026-11-13 08:00", "2026-11-13 22:00", "heat"}, // Fri after 07:30
	}
	for _, c := range cases {
		now, _ := time.ParseInLocation("2006-01-02 15:04", c.now, la)
		e, at, ok := s.Next(now)
		if !ok || at.Format("2006-01-02 15:04") != c.want || e.Mode != c.mode {
			t.Errorf("%s: got %v %v %v, want %s %s", c.now, at.Format("2006-01-02 15:04"), e.Mode, ok, c.want, c.mode)
		}
	}
	if _, _, ok := (Schedule{}).Next(time.Now()); ok {
		t.Error("empty schedule has no next event")
	}
	bad := Schedule{Events: []ScheduleEvent{{Days: dayCodes[:], StartTime: "99xx"}}}
	if _, _, ok := bad.Next(time.Now()); ok {
		t.Error("malformed start time must be ignored")
	}
}

func TestParseSchedule(t *testing.T) {
	s, err := ParseSchedule([]byte(`{"season":"Winter","events":[{"days":["Mo"],"startTime":"0730","operationMode":"heat","fanSpeed":null,"airDirection":null,"spHeat":20,"spCool":26}]}`))
	if err != nil || s.Season != "Winter" || len(s.Events) != 1 || s.Events[0].SpHeat == nil || *s.Events[0].SpHeat != 20 || s.Events[0].Fan != "" {
		t.Fatalf("%+v %v", s, err)
	}
}
