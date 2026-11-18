// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strconv"

	"github.com/hughobrien/comfortd/internal/store"
)

// LimitsView is the card's Min/Max setpoints control: the adapter's lowest
// cooling and highest heating setpoint, with the bounds the profile allows.
type LimitsView struct {
	Show           bool
	MinCool        string // current lowest cooling setpoint (empty if unknown)
	MaxHeat        string // current highest heating setpoint
	CoolLo, CoolHi string // bounds for MinCool
	HeatLo, HeatHi string // bounds for MaxHeat
}

// BuildLimits works out the control. It is shown only for units whose limits
// may be changed and whose profile is known.
func BuildLimits(s store.Snapshot, ctl CardControl) LimitsView {
	p := s.Profile
	if !ctl.Limits || p == nil {
		return LimitsView{}
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	v := LimitsView{Show: true,
		CoolLo: f(p.Min["cool"]), CoolHi: f(p.Max["cool"]), HeatLo: f(p.Min["heat"]), HeatHi: f(p.Max["heat"])}
	if lo, hi, ok := s.AdapterLimits(); ok {
		v.MinCool, v.MaxHeat = f(lo), f(hi)
	}
	return v
}
