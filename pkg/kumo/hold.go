// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"time"
)

// SetHold puts a zone in a hold. until is when it lapses; nil makes it
// permanent. The cloud wants endTime as a JSON number of epoch seconds (a
// string is a 400). Tests must only point this at kumotest.
func (c *Client) SetHold(ctx context.Context, siteID, zoneID string, until *time.Time) error {
	var end any // nil marshals as null: the permanent hold
	if until != nil {
		end = until.Unix()
	}
	_, err := c.do(ctx, "POST", "/v4/sites/"+siteID+"/hold", map[string]any{
		"siteId": siteID, "endTime": end,
		"zones": []map[string]any{{"id": zoneID, "enabled": true, "type": "hold"}},
	})
	return err
}

// EndHold ends a zone's hold. The unit goes back to the schedule's current
// event.
func (c *Client) EndHold(ctx context.Context, siteID, zoneID string) error {
	_, err := c.do(ctx, "POST", "/v4/sites/"+siteID+"/hold", map[string]any{
		"siteId": siteID,
		"zones":  []map[string]any{{"id": zoneID, "enabled": false, "type": "hold"}},
	})
	return err
}

// AwayHold is what a zone runs while Away mode is on (the Comfort app's "Away
// Settings"): the unit is set to this mode, setpoints, fan and vane until the
// away ends.
type AwayHold struct {
	OperationMode, FanSpeed, AirDirection string
	SpHeat, SpCool                        float64
}

func awayZone(zoneID string, enabled bool, a AwayHold) map[string]any {
	return map[string]any{"id": zoneID, "enabled": enabled, "type": "away",
		"operationMode": a.OperationMode, "fanSpeed": a.FanSpeed, "airDirection": a.AirDirection,
		"spHeat": a.SpHeat, "spCool": a.SpCool}
}

// SetAway puts a zone in Away mode with settings a. until is when it ends; nil
// leaves it on until it is ended. The body is the app's own (updateHoldMode
// with type "away" and the zone's away settings), read from the decompile.
// Tests must only point this at kumotest.
func (c *Client) SetAway(ctx context.Context, siteID, zoneID string, a AwayHold, until *time.Time) error {
	var end any
	if until != nil {
		end = until.Unix()
	}
	_, err := c.do(ctx, "POST", "/v4/sites/"+siteID+"/hold", map[string]any{
		"siteId": siteID, "endTime": end, "zones": []map[string]any{awayZone(zoneID, true, a)},
	})
	return err
}

// EndAway ends a zone's Away mode. As in the app, the end request names the
// type (away) and carries the same settings with enabled false.
func (c *Client) EndAway(ctx context.Context, siteID, zoneID string, a AwayHold) error {
	_, err := c.do(ctx, "POST", "/v4/sites/"+siteID+"/hold", map[string]any{
		"siteId": siteID, "zones": []map[string]any{awayZone(zoneID, false, a)},
	})
	return err
}
