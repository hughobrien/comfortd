// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
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
	On                                              bool
	Family                                          string   // mode with autoCool/autoHeat folded into auto
	ShowHeat, ShowCool                              bool     // which setpoint sliders apply in this mode
	Image                                           string   // model picture URL on this server
	Alerts                                          []string // active displayConfig flags, for the banner
	SpMin, SpMax                                    string   // heat setpoint range from the profile
	// Selectable values, offered only when the unit is writable and supports
	// them; Pressed marks the current one.
	Modes, Fans, Vanes []Choice
	Schedule           ScheduleView
	HoldUntil          string // "hold until today 22:00" while the app's temporary override is on, else empty
}

type Choice struct {
	Value, Label string
	Pressed      bool
}

func choices(cs []control.Choice, current string) []Choice {
	out := make([]Choice, len(cs))
	for i, c := range cs {
		out[i] = Choice{c.Value, c.Label, c.Value == current}
	}
	return out
}

const dash = "–"

// ImageKey names a model picture by its source URL; it is the cache file name
// and the ?v= that makes a changed picture a new URL.
func ImageKey(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:8])
}

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
		if strings.HasPrefix(d.ImageURL, "https://") {
			c.Image = "/ui/img/" + s.Unit.Slug + "?v=" + ImageKey(d.ImageURL) // served from our cache, never the CDN
		}
		if d.Display.Filter {
			c.Alerts = append(c.Alerts, "check filter")
		}
		if d.Display.Defrost {
			c.Alerts = append(c.Alerts, "defrosting")
		}
		if d.Display.Standby {
			c.Alerts = append(c.Alerts, "standby")
		}
		if d.Display.HotAdjust {
			c.Alerts = append(c.Alerts, "hot adjust")
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
	if ctl.Writable && s.Device != nil {
		d := s.Device
		c.On = d.Power == 1
		c.Family = control.ModeFamily(d.OperationMode)
		c.ShowHeat = c.Family == "heat" || c.Family == "auto"
		c.ShowCool = c.Family == "cool" || c.Family == "dry" || c.Family == "auto" // dry uses the cool setpoint
		field := control.FieldSpHeat
		if c.Family == "cool" || c.Family == "dry" {
			field = control.FieldSpCool
		}
		if lo, hi, ok := s.SetpointRange(control.RangeKey(field, c.Family)); ok {
			c.SpMin = strconv.FormatFloat(lo, 'f', -1, 64)
			c.SpMax = strconv.FormatFloat(hi, 'f', -1, 64)
		}
		if p := s.Profile; p != nil {
			c.Modes = choices(control.ModeChoices(*p), c.Family)
			c.Fans = choices(control.FanChoices(p.NumberOfFanSpeeds), d.FanSpeed)
			c.Vanes = choices(control.VaneChoices(*p), d.AirDirection)
		}
	}
	// The wall thermostat (House) speaks up only when its battery is not ok.
	var th struct {
		Battery string `json:"thermostatBattery"`
	}
	if json.Unmarshal(s.Raw[store.EndpointMhk2], &th) == nil && th.Battery != "" && th.Battery != "ok" {
		c.Alerts = append(c.Alerts, "thermostat battery "+th.Battery)
	}
	c.Schedule = BuildSchedule(s, now)
	if z := s.Zone; z != nil {
		c.ScheduleActive, c.HoldEnabled = z.HasActiveSchedule, z.HoldEnabled
		c.Schedule.Active = z.HasActiveSchedule
		if z.HoldEnabled {
			c.HoldUntil = "hold"
			if !z.HoldEnd.IsZero() {
				t := z.HoldEnd.In(scheduleLoc(s, now))
				c.HoldUntil = "hold until " + whenWords(now.In(t.Location()), t)
			}
		}
	}
	return c
}
