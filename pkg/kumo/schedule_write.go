// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"errors"
)

// AddScheduleEvents adds events to a zone's schedule in a season. The cloud
// adds them beside the existing events; it does not replace the schedule.
// Tests must only point this at kumotest.
func (c *Client) AddScheduleEvents(ctx context.Context, seasonID, zoneID string, events []ScheduleEvent) error {
	if len(events) == 0 {
		return errors.New("kumo: no events to add")
	}
	evs := make([]map[string]any, len(events))
	for i, e := range events {
		fan, vane := e.Fan, e.Vane
		if fan == "" {
			fan = "auto"
		}
		if vane == "" {
			vane = "auto"
		}
		evs[i] = map[string]any{
			"startTime": e.StartTime, "days": e.Days, "operationMode": e.Mode,
			"fanSpeed": fan, "airDirection": vane, "spHeat": e.SpHeat, "spCool": e.SpCool,
		}
	}
	_, err := c.do(ctx, "POST", "/v4/schedule-seasons/"+seasonID+"/schedules", map[string]any{
		"seasonId":  seasonID,
		"schedules": []map[string]any{{"zone": zoneID, "events": evs}},
	})
	return err
}

// DeleteScheduleEvents removes events (by id) from a zone's schedule.
func (c *Client) DeleteScheduleEvents(ctx context.Context, scheduleID string, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return errors.New("kumo: no events to delete")
	}
	_, err := c.do(ctx, "DELETE", "/v4/schedules/"+scheduleID+"/events", map[string]any{"events": eventIDs})
	return err
}
