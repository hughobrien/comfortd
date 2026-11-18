// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// LimitWriter sets a unit's Min/Max setpoint limits in the cloud.
type LimitWriter interface {
	SetLimits(ctx context.Context, serial string, minCool, maxHeat float64) error
}

// LimitsEnabled reports whether the unit's Min/Max setpoint limits may be
// changed: it must be writable and listed in Options.LimitUnits (not every
// adapter is known to take the command).
func (c *Controller) LimitsEnabled(slug string) bool {
	return c.o.Limits != nil && c.Writable(slug) && slices.Contains(c.o.LimitUnits, slug)
}

// SetLimits sets the unit's adapter limits: minCool is the lowest cooling
// setpoint, maxHeat the highest heating setpoint (°C, in 0.5 steps). Each must
// lie in the unit's own profile range for that mode, and minCool < maxHeat.
func (c *Controller) SetLimits(ctx context.Context, slug string, minCool, maxHeat float64, source string) error {
	if !c.LimitsEnabled(slug) {
		return ErrNotWritable
	}
	snap, ok := c.o.Store.Get(slug)
	if !ok {
		return fmt.Errorf("%w: unknown unit", ErrInvalid)
	}
	p := snap.Profile
	if p == nil || len(p.Min) == 0 {
		return fmt.Errorf("%w: the unit's profile is not known yet", ErrInvalid)
	}
	for _, v := range []float64{minCool, maxHeat} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v*2 != math.Round(v*2) {
			return fmt.Errorf("%w: limits are in steps of 0.5", ErrInvalid)
		}
	}
	if lo, hi := p.Min["cool"], p.Max["cool"]; minCool < lo || minCool > hi {
		return fmt.Errorf("%w: the lowest cooling setpoint must be %s to %s", ErrInvalid, fnum(lo), fnum(hi))
	}
	if lo, hi := p.Min["heat"], p.Max["heat"]; maxHeat < lo || maxHeat > hi {
		return fmt.Errorf("%w: the highest heating setpoint must be %s to %s", ErrInvalid, fnum(lo), fnum(hi))
	}
	if minCool >= maxHeat {
		return fmt.Errorf("%w: the lowest cooling setpoint must be below the highest heating setpoint", ErrInvalid)
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c.markSent(slug)
	if err := c.o.Limits.SetLimits(sctx, snap.Unit.Serial, minCool, maxHeat); err != nil {
		return err
	}
	oldMin, oldMax := "null", "null"
	if lo, hi, ok := snap.AdapterLimits(); ok {
		oldMin, oldMax = fnum(lo), fnum(hi)
	}
	c.record(slug, "status", "minSetPoint", oldMin, fnum(minCool), source)
	c.record(slug, "status", "maxSetPoint", oldMax, fnum(maxHeat), source)
	if c.o.AfterLimits != nil {
		c.o.AfterLimits(ctx)
	}
	return nil
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
