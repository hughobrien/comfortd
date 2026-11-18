// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"fmt"
)

// MinMaxPreference is the account preference that turns the Comfort app's
// "Min/Max setpoints" feature on. It is account-wide, not per unit.
const MinMaxPreference = "isMinMaxSetpointsEnabled"

// Preferences returns the account's preferences object (GET /accounts/me).
func (c *Client) Preferences(ctx context.Context) (map[string]any, error) {
	raw, err := c.do(ctx, "GET", "/accounts/me", nil)
	if err != nil {
		return nil, err
	}
	var me struct {
		Preferences map[string]any `json:"preferences"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return nil, fmt.Errorf("accounts/me: %w", err)
	}
	if me.Preferences == nil {
		return nil, fmt.Errorf("accounts/me: no preferences")
	}
	return me.Preferences, nil
}

// MinMaxEnabled reads the Min/Max setpoints preference.
func (c *Client) MinMaxEnabled(ctx context.Context) (bool, error) {
	p, err := c.Preferences(ctx)
	if err != nil {
		return false, err
	}
	on, _ := p[MinMaxPreference].(bool)
	return on, nil
}

// SetMinMaxEnabled turns the Min/Max setpoints preference on or off, as the app
// does: it reads the account's preferences, changes the one flag, and PUTs the
// whole object back (PUT /accounts/preferences). Tests must only point this at
// kumotest.
func (c *Client) SetMinMaxEnabled(ctx context.Context, on bool) error {
	p, err := c.Preferences(ctx)
	if err != nil {
		return err
	}
	p[MinMaxPreference] = on
	_, err = c.do(ctx, "PUT", "/accounts/preferences", p)
	return err
}
