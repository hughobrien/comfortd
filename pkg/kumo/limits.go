// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import "context"

// SetLimits sets a unit's Min/Max setpoint limits on its adapter: the lowest
// cooling setpoint and the highest heating setpoint, in °C. It is the call the
// Comfort app's "Min/Max setpoints" screen makes: a relay command to the
// adapter, whose result shows in the status endpoint's minSetPoint and
// maxSetPoint. Tests must only point this at kumotest.
func (c *Client) SetLimits(ctx context.Context, serial string, minCool, maxHeat float64) error {
	_, err := c.do(ctx, "POST", "/devices/"+serial+"/relay-command", map[string]any{
		"serial": serial,
		"adapter": map[string]any{"status": map[string]any{
			"userMinCoolSetPoint": minCool, "userMaxHeatSetPoint": maxHeat,
		}},
	})
	return err
}
