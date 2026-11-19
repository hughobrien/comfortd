// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"log/slog"
)

// PrefsClient reads and writes the account preference that turns the Comfort
// app's Min/Max setpoints feature on (it is account-wide, not per unit).
type PrefsClient interface {
	MinMaxEnabled(ctx context.Context) (bool, error)
	SetMinMaxEnabled(ctx context.Context, on bool) error
}

// LimitsOn reports the Min/Max setpoints preference as last read; known is
// false until a read has worked.
func (c *Controller) LimitsOn() (on, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitsOn, c.limitsOnKnown
}

// RefreshPrefs reads the preference from the cloud.
func (c *Controller) RefreshPrefs(ctx context.Context) {
	if c.o.Prefs == nil {
		return
	}
	on, err := c.o.Prefs.MinMaxEnabled(ctx)
	if err != nil {
		slog.Warn("read min/max preference", "err", err)
		return
	}
	c.mu.Lock()
	c.limitsOn, c.limitsOnKnown = on, true
	c.mu.Unlock()
	c.changedAll()
}

// SetLimitsOn turns the Min/Max setpoints feature on or off (for the whole
// account, as in the app).
func (c *Controller) SetLimitsOn(ctx context.Context, slug string, on bool) error {
	if !c.LimitsEnabled(slug) || c.o.Prefs == nil {
		return ErrUnavailable
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := c.o.Prefs.SetMinMaxEnabled(sctx, on); err != nil {
		return err
	}
	slog.Info("min/max setpoints preference changed", "on", on)
	c.mu.Lock()
	c.limitsOn, c.limitsOnKnown = on, true
	c.mu.Unlock()
	c.changedAll()
	return nil
}

func (c *Controller) changedAll() {
	if c.o.OnChange == nil {
		return
	}
	for _, u := range c.o.Store.Units() {
		c.o.OnChange(u.Slug)
	}
}
