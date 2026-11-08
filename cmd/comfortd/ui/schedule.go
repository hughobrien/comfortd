// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

// ScheduleView is the card's read-only schedule section.
type ScheduleView struct {
	Present bool
	Season  string
	Next    string // "tonight 22:00 → Heat 19 °C", empty when there is no next event
	Rows    []ScheduleRow
	Error   string // why there is no schedule, when the fetch failed
}

type ScheduleRow struct {
	Days, At, Mode, Target, Fan string
	IsNext                      bool
}

var scheduleModeLabels = map[string]string{
	"heat": "Heat", "cool": "Cool", "dry": "Dry", "vent": "Fan", "auto": "Auto", "off": "Off",
}

var scheduleFanLabels = map[string]string{
	"auto": "Auto", "superQuiet": "Quiet", "quiet": "Low", "low": "Medium", "powerful": "High", "superPowerful": "Powerful",
}

// weekOrder lists the day codes Monday first, the way people read a week.
var weekOrder = []string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"}

var dayNames = map[string]string{"Mo": "Mon", "Tu": "Tue", "We": "Wed", "Th": "Thu", "Fr": "Fri", "Sa": "Sat", "Su": "Sun"}

// scheduleDays words a set of day codes: "Every day", "Mon–Fri", "Sat, Sun".
func scheduleDays(days []string) string {
	var in []string
	for _, d := range weekOrder {
		if slices.Contains(days, d) {
			in = append(in, d)
		}
	}
	switch {
	case len(in) == 7:
		return "Every day"
	case slices.Equal(in, weekOrder[:5]):
		return "Mon–Fri"
	}
	names := make([]string, len(in))
	for i, d := range in {
		names[i] = dayNames[d]
	}
	return strings.Join(names, ", ")
}

func fmtClock(hhmm string) string {
	if len(hhmm) == 4 {
		return hhmm[:2] + ":" + hhmm[2:]
	}
	return hhmm
}

// scheduleTarget words the event's setpoints for its mode.
func scheduleTarget(e kumo.ScheduleEvent) string {
	switch e.Mode {
	case "heat":
		return fmtTempPtr(e.SpHeat)
	case "cool", "dry":
		return fmtTempPtr(e.SpCool)
	case "auto":
		return fmtTempPtr(e.SpHeat) + " – " + fmtTempPtr(e.SpCool)
	}
	return dash
}

func fmtTempPtr(v *float64) string {
	if v == nil {
		return dash
	}
	return fmtTemp(v) + " °C"
}

func modeLabel(m string) string {
	if l, ok := scheduleModeLabels[m]; ok {
		return l
	}
	if m == "" {
		return dash
	}
	return m
}

// whenWords says when t is relative to now, both in the same location.
func whenWords(now, t time.Time) string {
	days := int(t.Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())).Hours() / 24)
	clock := t.Format("15:04")
	switch days {
	case 0:
		return "today " + clock
	case 1:
		return "tomorrow " + clock
	}
	return t.Format("Mon") + " " + clock
}

// BuildSchedule builds the section from the unit's stored schedule. now is in
// the viewer's zone; times are shown in the unit's own zone.
func BuildSchedule(s store.Snapshot, now time.Time) ScheduleView {
	raw, ok := s.Raw[store.EndpointSchedule]
	if !ok {
		return ScheduleView{Error: s.Errors[store.EndpointSchedule]}
	}
	sch, err := kumo.ParseSchedule(raw)
	if err != nil {
		return ScheduleView{Error: err.Error()}
	}
	v := ScheduleView{Present: true, Season: sch.Season}
	if s.Device != nil && s.Device.TimeZone != "" {
		if loc, err := time.LoadLocation(s.Device.TimeZone); err == nil {
			now = now.In(loc)
		}
	}
	next, at, hasNext := sch.Next(now)
	if hasNext {
		v.Next = whenWords(now, at) + " → " + modeLabel(next.Mode)
		if t := scheduleTarget(next); t != dash && next.Mode != "off" {
			v.Next += " " + t
		}
	}
	events := slices.Clone(sch.Events)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].StartTime != events[j].StartTime {
			return events[i].StartTime < events[j].StartTime
		}
		return scheduleDays(events[i].Days) < scheduleDays(events[j].Days)
	})
	for _, e := range events {
		r := ScheduleRow{Days: scheduleDays(e.Days), At: fmtClock(e.StartTime), Mode: modeLabel(e.Mode), Target: scheduleTarget(e), Fan: dash}
		if l, ok := scheduleFanLabels[e.Fan]; ok {
			r.Fan = l
		}
		r.IsNext = hasNext && e.StartTime == next.StartTime && slices.Equal(e.Days, next.Days)
		v.Rows = append(v.Rows, r)
	}
	return v
}
