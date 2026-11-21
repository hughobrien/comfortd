// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"fmt"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

// HoldWriter changes a zone's hold in the cloud.
type HoldWriter interface {
	SetHold(ctx context.Context, siteID, zoneID string, until *time.Time) error
	EndHold(ctx context.Context, siteID, zoneID string) error
	SetAway(ctx context.Context, siteID, zoneID string, a kumo.AwayHold, until *time.Time) error
	EndAway(ctx context.Context, siteID, zoneID string, a kumo.AwayHold) error
}

// holdIDs checks the unit can have its hold changed and returns its snapshot.
func (c *Controller) holdIDs(slug string) (store.Snapshot, error) {
	var none store.Snapshot
	if c.o.Holds == nil {
		return none, ErrUnavailable
	}
	snap, ok := c.o.Store.Get(slug)
	if !ok {
		return none, fmt.Errorf("%w: unknown unit", ErrInvalid)
	}
	if snap.Unit.SiteID == "" || snap.Unit.ZoneID == "" {
		return none, fmt.Errorf("%w: the unit has no zone to hold", ErrInvalid)
	}
	return snap, nil
}

// holdState words a zone's hold for the history: {enabled, holdType, endTime}.
func holdState(s store.Snapshot) string {
	if s.Zone == nil {
		return "null"
	}
	m := map[string]any{"enabled": s.Zone.HoldEnabled, "holdType": s.Zone.HoldType}
	if s.Zone.HoldKind == "away" {
		m["type"] = "away"
	}
	if !s.Zone.HoldEnd.IsZero() {
		m["endTime"] = s.Zone.HoldEnd.UTC().Format(time.RFC3339)
	}
	return jsonOf(m)
}

// SetHold holds the unit's settings until until; nil makes the hold permanent.
func (c *Controller) SetHold(ctx context.Context, slug string, until *time.Time, source string) error {
	unlock, ctx, err := c.lockWrites(ctx, slug)
	if err != nil {
		return err
	}
	defer unlock()
	snap, err := c.holdIDs(slug)
	if err != nil {
		return err
	}
	if until != nil && !until.After(c.o.Now()) {
		return fmt.Errorf("%w: the hold must end in the future", ErrInvalid)
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c.markSent(slug)
	if err := c.o.Holds.SetHold(sctx, snap.Unit.SiteID, snap.Unit.ZoneID, until); err != nil {
		return err
	}
	next := map[string]any{"enabled": true, "holdType": "permanent"}
	if until != nil {
		next["holdType"], next["endTime"] = "until_specific_time", until.UTC().Format(time.RFC3339)
	}
	c.record(slug, "hold", "holdMode", holdState(snap), jsonOf(next), source)
	c.afterHold(ctx, slug)
	return nil
}

// EndHold ends the unit's hold; the unit goes back to the schedule's current event.
func (c *Controller) EndHold(ctx context.Context, slug, source string) error {
	unlock, ctx, err := c.lockWrites(ctx, slug)
	if err != nil {
		return err
	}
	defer unlock()
	snap, err := c.holdIDs(slug)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c.markSent(slug)
	var err2 error
	if z := snap.Zone; z != nil && z.HoldEnabled && z.HoldKind == "away" {
		err2 = c.o.Holds.EndAway(sctx, snap.Unit.SiteID, snap.Unit.ZoneID, AwaySettings(snap))
	} else {
		err2 = c.o.Holds.EndHold(sctx, snap.Unit.SiteID, snap.Unit.ZoneID)
	}
	if err2 != nil {
		return err2
	}
	c.record(slug, "hold", "holdMode", holdState(snap), jsonOf(map[string]any{"enabled": false}), source)
	c.afterHold(ctx, slug)
	return nil
}

// afterHold has the poller look again: the zone now (that is where the hold
// shows), the device shortly after (a hold change can move the unit).
func (c *Controller) afterHold(ctx context.Context, slug string) {
	if c.o.Kick != nil {
		c.o.Kick(slug)
	}
	c.refresh(ctx, c.o.AfterHold)
}

// SetAway turns Away mode on for the unit's zone, with the unit's away
// settings (see AwaySettings); until is when it ends, nil for until ended.
func (c *Controller) SetAway(ctx context.Context, slug string, until *time.Time, source string) error {
	unlock, ctx, err := c.lockWrites(ctx, slug)
	if err != nil {
		return err
	}
	defer unlock()
	snap, err := c.holdIDs(slug)
	if err != nil {
		return err
	}
	if until != nil && !until.After(c.o.Now()) {
		return fmt.Errorf("%w: the away must end in the future", ErrInvalid)
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c.markSent(slug)
	if err := c.o.Holds.SetAway(sctx, snap.Unit.SiteID, snap.Unit.ZoneID, AwaySettings(snap), until); err != nil {
		return err
	}
	next := map[string]any{"enabled": true, "type": "away", "holdType": "permanent"}
	if until != nil {
		next["holdType"], next["endTime"] = "until_specific_time", until.UTC().Format(time.RFC3339)
	}
	c.record(slug, "hold", "holdMode", holdState(snap), jsonOf(next), source)
	c.afterHold(ctx, slug)
	return nil
}

// AwaySettings are what the unit runs while Away is on. They follow the app's
// defaults (auto, heat 17, cool 26, fan and vane auto), kept inside the unit's
// own limits: House's auto range starts at 19, so its away heat setpoint is 19.
// Where the unit has no auto mode, heat is used instead.
func AwaySettings(s store.Snapshot) kumo.AwayHold {
	a := kumo.AwayHold{OperationMode: "auto", FanSpeed: "auto", AirDirection: "auto", SpHeat: 17, SpCool: 26}
	if p := s.Profile; p != nil {
		key := "auto"
		if !p.SupportsAuto() {
			a.OperationMode, key = "heat", "heat"
		}
		if lo, ok := p.Min[key]; ok {
			a.SpHeat = max(a.SpHeat, lo)
		}
		if hi, ok := p.Max[key]; ok {
			a.SpCool = min(a.SpCool, hi)
		}
		if lo, hi, ok := s.SetpointRange(key); ok { // include the adapter limits
			a.SpHeat, a.SpCool = max(a.SpHeat, lo), min(a.SpCool, hi)
		}
	}
	return a
}
