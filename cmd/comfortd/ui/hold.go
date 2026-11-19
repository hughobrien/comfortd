// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"errors"
	"fmt"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

// ErrBadTime marks a clock time that could not be read.
var ErrBadTime = errors.New("bad time")

// HoldView is the card's hold control: which radio is on and what the
// choices need.
type HoldView struct {
	Show    bool      // the unit has a zone
	Mode    string    // none, next, until, permanent or away (Away mode is on)
	Time    string    // "15:04" in the unit's zone: the end of an "until" hold, else empty
	CanNext bool      // the schedule has a next event to hold until
	NextAt  time.Time // that event
}

// BuildHold works out the control from the zone's hold and the schedule. A
// permanent hold's far-future end time is never shown.
func BuildHold(s store.Snapshot, now time.Time) HoldView {
	if s.Zone == nil {
		return HoldView{}
	}
	sc := BuildSchedule(s, now)
	h := HoldView{Show: true, Mode: "none", CanNext: !sc.NextAt.IsZero(), NextAt: sc.NextAt}
	zz, _ := s.Zone.WithAdapterHold(now)
	z := &zz
	if !z.HoldEnabled {
		return h
	}
	if z.HoldKind == "away" {
		h.Mode = "away"
		return h
	}
	until := func() {
		h.Mode = "until"
		if !z.HoldEnd.IsZero() {
			h.Time = z.HoldEnd.In(scheduleLoc(s, now)).Format("15:04")
		}
	}
	switch z.HoldType {
	case "permanent":
		h.Mode = "permanent"
	case "until_next_event":
		h.Mode = "next"
	case "until_specific_time":
		if h.CanNext && z.HoldEnd.Equal(h.NextAt) {
			h.Mode = "next"
		} else {
			until()
		}
	default:
		until()
	}
	return h
}

// HoldTimeFromClock turns "HH:MM" into the next time that clock reads in the
// unit's zone: today if it is still ahead, else tomorrow.
func HoldTimeFromClock(s store.Snapshot, now time.Time, hhmm string) (time.Time, error) {
	c, err := time.Parse("15:04", hhmm)
	if err != nil || len(hhmm) != 5 {
		return time.Time{}, fmt.Errorf("%w: %q is not a time (HH:MM)", ErrBadTime, hhmm)
	}
	loc := scheduleLoc(s, now)
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), c.Hour(), c.Minute(), 0, 0, loc)
	if !t.After(n) {
		t = time.Date(n.Year(), n.Month(), n.Day()+1, c.Hour(), c.Minute(), 0, 0, loc)
	}
	return t, nil
}
