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
