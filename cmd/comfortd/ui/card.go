// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strconv"
	"time"

	"github.com/hughobrien/comfortd/internal/annotate"
	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/store"
)

// CardControl carries control state into the card.
type CardControl struct {
	Writable  bool
	Pending   map[string]string // field → display value
	LastError string
}

type CardView struct {
	Slug                                            string
	Control                                         CardControl
	Mode, Power, SpHeat, SpCool, RoomTemp, Humidity string // "–" when missing
	Connected, Stale, ScheduleActive, HoldEnabled   bool
	LastOK                                          string // "15:04:05", or "never"
	Fan, Vane                                       string
	FanOptions, VaneOptions                         []string // offered only when the unit supports them
}

const dash = "–"

func fmtTemp(v *float64) string {
	if v == nil {
		return dash
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func BuildCard(s store.Snapshot, ann *annotate.Set, ctl CardControl, now time.Time, interval time.Duration) CardView {
	c := CardView{Slug: s.Unit.Slug, Control: ctl, Mode: dash, Power: dash, SpHeat: dash, SpCool: dash, RoomTemp: dash, Humidity: dash, Fan: dash, Vane: dash,
		Stale: s.Stale(now, interval), LastOK: "never"}
	if !s.LastDeviceOK.IsZero() {
		c.LastOK = s.LastDeviceOK.Local().Format("15:04:05")
	}
	if d := s.Device; d != nil {
		c.Connected = d.Connected
		c.SpHeat, c.SpCool, c.RoomTemp, c.Humidity = fmtTemp(d.SpHeat), fmtTemp(d.SpCool), fmtTemp(d.RoomTemp), fmtTemp(d.Humidity)
		if d.OperationMode != "" {
			c.Mode = d.OperationMode
			if f, ok := ann.Lookup(store.EndpointDevice, "operationMode"); ok {
				if m, ok := f.Decode(strconv.Quote(d.OperationMode)); ok {
					c.Mode = m
				}
			}
		}
		if d.FanSpeed != "" {
			c.Fan = d.FanSpeed
		}
		if d.AirDirection != "" {
			c.Vane = d.AirDirection
		}
		if d.Power == 1 {
			c.Power = "On"
		} else {
			c.Power = "Off"
		}
	}
	if p := s.Profile; p != nil {
		if n := p.NumberOfFanSpeeds; n > 0 {
			c.FanOptions = append([]string{"auto"}, control.FanSpeeds(n)...)
		}
		if p.HasVaneDir {
			c.VaneOptions = append(c.VaneOptions, "auto", "horizontal", "midhorizontal", "midpoint", "midvertical", "vertical")
		}
		if p.HasVaneSwing {
			c.VaneOptions = append(c.VaneOptions, "swing")
		}
	}
	if z := s.Zone; z != nil {
		c.ScheduleActive, c.HoldEnabled = z.HasActiveSchedule, z.HoldEnabled
	}
	return c
}
