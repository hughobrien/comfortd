// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

// ScheduleView is the card's read-only schedule section.
type ScheduleView struct {
	Present bool
	Active  bool // the zone's schedule is switched on (hasActiveSchedule)
	Season  string
	Heading string    // "Winter season · America/Los_Angeles": the season and the unit's own time zone
	Next    string    // "tonight 22:00 → Heat 19 °C", empty when there is no next event
	NextAt  time.Time // when that event fires, in the unit's zone; zero when there is none
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

// scheduleLoc is the unit's own time zone (schedule times and holds are in it),
// else now's.
func scheduleLoc(s store.Snapshot, now time.Time) *time.Location {
	if s.Device != nil && s.Device.TimeZone != "" {
		if loc, err := time.LoadLocation(s.Device.TimeZone); err == nil {
			return loc
		}
	}
	return now.Location()
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
	loc := scheduleLoc(s, now)
	v := ScheduleView{Present: true, Season: sch.Season, Heading: sch.Season + " season · " + loc.String()}
	now = now.In(loc)
	next, at, hasNext := sch.Next(now)
	if hasNext {
		v.NextAt = at
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

// EditRow is one row of the schedule editor. Key is the event's cloud id, or a
// fresh "n…" key for a row added in the editor.
type EditRow struct {
	Key, At, Mode, SpHeat, SpCool, Fan, Vane string
	Days                                     map[string]bool
}

// DayChip is one weekday checkbox.
type DayChip struct{ Code, Short string }

var DayChips = []DayChip{{"Mo", "M"}, {"Tu", "T"}, {"We", "W"}, {"Th", "T"}, {"Fr", "F"}, {"Sa", "S"}, {"Su", "S"}}

// EditOpts is what the editor offers for one unit: its modes, fan speeds and
// setpoint range, derived from the unit's own data.
type EditOpts struct {
	Modes, Fans []Choice
	Lo, Hi      string
}

// BuildEditOpts derives the editor's choices from the snapshot.
func BuildEditOpts(s store.Snapshot) EditOpts {
	l := control.ScheduleLimitsFor(s)
	return EditOpts{Modes: choices(l.Modes, ""), Fans: choices(l.Fans, ""), Lo: fnumStr(l.Lo), Hi: fnumStr(l.Hi)}
}

func fnumStr(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// NewEditRow is a blank row: weekdays at 08:00, heat 20.
func NewEditRow(key string) EditRow {
	return EditRow{Key: key, At: "08:00", Mode: "heat", SpHeat: "20", SpCool: "26", Fan: "auto", Vane: "auto",
		Days: map[string]bool{"Mo": true, "Tu": true, "We": true, "Th": true, "Fr": true}}
}

func editTemp(v *float64, def string) string {
	if v == nil {
		return def
	}
	return fmtTemp(v)
}

// BuildScheduleEdit lists the unit's stored events as editor rows, in the
// same order as the read-only table.
func BuildScheduleEdit(s store.Snapshot) []EditRow {
	raw, ok := s.Raw[store.EndpointSchedule]
	if !ok {
		return nil
	}
	sch, err := kumo.ParseSchedule(raw)
	if err != nil {
		return nil
	}
	events := slices.Clone(sch.Events)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].StartTime != events[j].StartTime {
			return events[i].StartTime < events[j].StartTime
		}
		return scheduleDays(events[i].Days) < scheduleDays(events[j].Days)
	})
	var out []EditRow
	for _, e := range events {
		r := EditRow{Key: e.ID, At: fmtClock(e.StartTime), Mode: e.Mode, Fan: e.Fan, Vane: e.Vane,
			SpHeat: editTemp(e.SpHeat, "20"), SpCool: editTemp(e.SpCool, "26"), Days: map[string]bool{}}
		if r.Fan == "" {
			r.Fan = "auto"
		}
		if r.Vane == "" {
			r.Vane = "auto"
		}
		for _, d := range e.Days {
			r.Days[d] = true
		}
		out = append(out, r)
	}
	return out
}
