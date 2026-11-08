// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

// ScheduleWriter changes a zone's schedule in the cloud.
type ScheduleWriter interface {
	AddScheduleEvents(ctx context.Context, seasonID, zoneID string, events []kumo.ScheduleEvent) error
	DeleteScheduleEvents(ctx context.Context, scheduleID string, eventIDs []string) error
}

var (
	scheduleModes = []string{"heat", "cool", "auto", "dry", "vent", "off"}
	scheduleFans  = []string{"auto", "superQuiet", "quiet", "low", "powerful", "superPowerful"}
	scheduleDays  = []string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"}
)

const (
	scheduleMinSp = 10.0
	scheduleMaxSp = 31.0
)

// ValidateSchedule checks edited events and returns them with fields cleaned
// (fan and vane defaulted, days in week order). Errors wrap ErrInvalid.
func ValidateSchedule(events []kumo.ScheduleEvent) ([]kumo.ScheduleEvent, error) {
	bad := func(i int, f string, a ...any) error {
		return fmt.Errorf("%w: row %d: %s", ErrInvalid, i+1, fmt.Sprintf(f, a...))
	}
	out := make([]kumo.ScheduleEvent, len(events))
	for i, e := range events {
		var h, m int
		if _, err := fmt.Sscanf(e.StartTime, "%02d%02d", &h, &m); err != nil || len(e.StartTime) != 4 || h > 23 || m > 59 {
			return nil, bad(i, "time %q is not HH:MM", e.StartTime)
		}
		if !slices.Contains(scheduleModes, e.Mode) {
			return nil, bad(i, "mode %q", e.Mode)
		}
		if e.Fan == "" {
			e.Fan = "auto"
		}
		if !slices.Contains(scheduleFans, e.Fan) {
			return nil, bad(i, "fan %q", e.Fan)
		}
		if e.Vane == "" {
			e.Vane = "auto"
		}
		var days []string
		for _, d := range scheduleDays {
			if slices.Contains(e.Days, d) {
				days = append(days, d)
			}
		}
		if len(days) == 0 || len(days) != len(e.Days) {
			return nil, bad(i, "pick at least one day")
		}
		e.Days = days
		if e.SpHeat == nil || e.SpCool == nil {
			return nil, bad(i, "needs both temperatures")
		}
		for _, v := range []float64{*e.SpHeat, *e.SpCool} {
			if v < scheduleMinSp || v > scheduleMaxSp || v*2 != float64(int(v*2)) {
				return nil, bad(i, "temperature %.1f must be %.0f–%.0f in steps of 0.5", v, scheduleMinSp, scheduleMaxSp)
			}
		}
		if e.Mode == "auto" && *e.SpCool-*e.SpHeat < MinAutoGap {
			return nil, bad(i, "auto needs cool at least %.1f above heat", MinAutoGap)
		}
		out[i] = e
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[i].StartTime != out[j].StartTime {
				continue
			}
			for _, d := range out[i].Days {
				if slices.Contains(out[j].Days, d) {
					return nil, fmt.Errorf("%w: rows %d and %d both fire at %s on %s", ErrInvalid, i+1, j+1, fmtHHMM(out[i].StartTime), d)
				}
			}
		}
	}
	return out, nil
}

func fmtHHMM(s string) string { return s[:2] + ":" + s[2:] }

func sameEvent(a, b kumo.ScheduleEvent) bool {
	norm := func(e kumo.ScheduleEvent) string {
		d := slices.Clone(e.Days)
		sort.Strings(d)
		fan, vane := e.Fan, e.Vane
		if fan == "" {
			fan = "auto"
		}
		if vane == "" {
			vane = "auto"
		}
		f := func(v *float64) string {
			if v == nil {
				return "-"
			}
			return fmt.Sprint(*v)
		}
		return strings.Join([]string{strings.Join(d, ","), e.StartTime, e.Mode, fan, vane, f(e.SpHeat), f(e.SpCool)}, "|")
	}
	return norm(a) == norm(b)
}

// ScheduleChange is what ApplySchedule did.
type ScheduleChange struct{ Added, Deleted int }

// ApplySchedule makes the unit's schedule equal to edited. Rows keep the id of
// the event they came from (empty for new rows); a row that differs from its
// original is added as a new event and the original deleted, rows that are
// gone are deleted. Adds go first so a failure part-way never leaves a gap.
func (c *Controller) ApplySchedule(ctx context.Context, slug string, edited []kumo.ScheduleEvent, source string) (ScheduleChange, error) {
	var none ScheduleChange
	if !c.Writable(slug) || c.o.Schedules == nil {
		return none, ErrNotWritable
	}
	snap, ok := c.o.Store.Get(slug)
	if !ok {
		return none, fmt.Errorf("%w: unknown unit", ErrInvalid)
	}
	raw, ok := snap.Raw["schedule"]
	if !ok {
		return none, fmt.Errorf("%w: the schedule has not been read yet", ErrInvalid)
	}
	cur, err := kumo.ParseSchedule(raw)
	if err != nil {
		return none, err
	}
	if cur.SeasonID == "" || cur.ScheduleID == "" || snap.Unit.ZoneID == "" {
		return none, fmt.Errorf("%w: the schedule has no ids to edit", ErrInvalid)
	}
	edited, err = ValidateSchedule(edited)
	if err != nil {
		return none, err
	}
	byID := map[string]kumo.ScheduleEvent{}
	for _, e := range cur.Events {
		byID[e.ID] = e
	}
	var add []kumo.ScheduleEvent
	del := map[string]bool{}
	kept := map[string]bool{}
	for _, e := range edited {
		orig, had := byID[e.ID]
		switch {
		case e.ID == "":
			add = append(add, e)
		case !had:
			return none, fmt.Errorf("%w: the schedule changed since it was loaded; cancel and edit again", ErrInvalid)
		case sameEvent(orig, e):
			kept[e.ID] = true
		default:
			add = append(add, e)
			del[e.ID] = true
		}
	}
	for _, e := range cur.Events {
		if !kept[e.ID] && !slices.ContainsFunc(edited, func(x kumo.ScheduleEvent) bool { return x.ID == e.ID }) {
			del[e.ID] = true
		}
	}
	if len(add) == 0 && len(del) == 0 {
		return none, nil
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if len(add) > 0 {
		for i := range add {
			add[i].ID = ""
		}
		if err := c.o.Schedules.AddScheduleEvents(sctx, cur.SeasonID, snap.Unit.ZoneID, add); err != nil {
			return none, err
		}
		for _, e := range add {
			c.record(slug, "schedule", "event", "null", jsonOf(e), source)
		}
	}
	ch := ScheduleChange{Added: len(add)}
	var dels []string
	for id := range del {
		dels = append(dels, id)
	}
	sort.Strings(dels)
	if len(dels) > 0 {
		err := c.o.Schedules.DeleteScheduleEvents(sctx, cur.ScheduleID, dels)
		if err == nil {
			for _, id := range dels {
				c.record(slug, "schedule", "event", jsonOf(byID[id]), "null", source)
			}
			ch.Deleted = len(dels)
		}
		if c.o.AfterSchedule != nil {
			c.o.AfterSchedule(ctx)
		}
		if err != nil {
			return ch, err
		}
		return ch, nil
	}
	if c.o.AfterSchedule != nil {
		c.o.AfterSchedule(ctx)
	}
	return ch, nil
}
