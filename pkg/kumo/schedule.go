// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// ScheduleEvent is one entry of a zone's schedule: at StartTime on the given
// days the unit is set to the mode, fan and setpoints (nil/empty = unchanged).
type ScheduleEvent struct {
	ID        string   `json:"id,omitempty"` // the cloud's id; empty for an event not yet created
	Days      []string `json:"days"`         // "Su" "Mo" "Tu" "We" "Th" "Fr" "Sa"
	StartTime string   `json:"startTime"`    // "HHMM", site local time
	Mode      string   `json:"operationMode"`
	Fan       string   `json:"fanSpeed"`
	Vane      string   `json:"airDirection"`
	SpHeat    *float64 `json:"spHeat"`
	SpCool    *float64 `json:"spCool"`
}

// Schedule is a zone's events in the running season. The cloud returns the
// events unsorted.
type Schedule struct {
	Season     string          `json:"season"`
	SeasonID   string          `json:"seasonId"`
	ScheduleID string          `json:"scheduleId"` // the zone's schedule within the season
	Events     []ScheduleEvent `json:"events"`
}

var dayCodes = [7]string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"}

// DayCode is the cloud's two-letter code for a weekday.
func DayCode(d time.Weekday) string { return dayCodes[d] }

// ParseSchedule parses the stored schedule JSON ({"season":…, "events":[…]}).
func ParseSchedule(raw json.RawMessage) (Schedule, error) {
	var s Schedule
	if err := json.Unmarshal(raw, &s); err != nil {
		return Schedule{}, fmt.Errorf("kumo: parse schedule: %w", err)
	}
	return s, nil
}

// clock returns the event's start as minutes after midnight.
func (e ScheduleEvent) clock() (h, m int, ok bool) {
	if len(e.StartTime) != 4 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(e.StartTime, "%02d%02d", &h, &m); err != nil || h > 23 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// At is the event's time on the calendar day of base, in base's location.
func (e ScheduleEvent) At(base time.Time) (time.Time, bool) {
	h, m, ok := e.clock()
	if !ok {
		return time.Time{}, false
	}
	return time.Date(base.Year(), base.Month(), base.Day(), h, m, 0, 0, base.Location()), true
}

// Next is the first event strictly after now (in now's location), or false
// when the schedule has no usable events.
func (s Schedule) Next(now time.Time) (ScheduleEvent, time.Time, bool) {
	var best ScheduleEvent
	var bestAt time.Time
	for d := 0; d <= 7; d++ {
		day := now.AddDate(0, 0, d)
		for _, e := range s.Events {
			if !slices.Contains(e.Days, DayCode(day.Weekday())) {
				continue
			}
			t, ok := e.At(day)
			if !ok || !t.After(now) {
				continue
			}
			if bestAt.IsZero() || t.Before(bestAt) {
				best, bestAt = e, t
			}
		}
		if !bestAt.IsZero() {
			break // days are scanned in order, so the first hit day has the earliest
		}
	}
	return best, bestAt, !bestAt.IsZero()
}
