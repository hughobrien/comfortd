// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"fmt"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

// HoldWriter changes a zone's hold in the cloud.
type HoldWriter interface {
	SetHold(ctx context.Context, siteID, zoneID string, until *time.Time) error
	EndHold(ctx context.Context, siteID, zoneID string) error
}

// holdIDs checks the unit can have its hold changed and returns its snapshot.
func (c *Controller) holdIDs(slug string) (store.Snapshot, error) {
	var none store.Snapshot
	if !c.Writable(slug) || c.o.Holds == nil {
		return none, ErrNotWritable
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
	if !s.Zone.HoldEnd.IsZero() {
		m["endTime"] = s.Zone.HoldEnd.UTC().Format(time.RFC3339)
	}
	return jsonOf(m)
}

// SetHold holds the unit's settings until until; nil makes the hold permanent.
func (c *Controller) SetHold(ctx context.Context, slug string, until *time.Time, source string) error {
	snap, err := c.holdIDs(slug)
	if err != nil {
		return err
	}
	if until != nil && !until.After(c.o.Now()) {
		return fmt.Errorf("%w: the hold must end in the future", ErrInvalid)
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
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
	snap, err := c.holdIDs(slug)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := c.o.Holds.EndHold(sctx, snap.Unit.SiteID, snap.Unit.ZoneID); err != nil {
		return err
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
	if c.o.AfterHold != nil {
		c.o.AfterHold(ctx)
	}
}
