// SPDX-License-Identifier: GPL-3.0-or-later

// Package homekit exposes units to Apple Home as HeaterCooler accessories.
package homekit

import (
	"math"
	"slices"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

// HeaterCooler characteristic values.
const (
	TargetAuto = 0
	TargetHeat = 1
	TargetCool = 2

	CurrentInactive = 0
	CurrentIdle     = 1
	CurrentHeating  = 2
	CurrentCooling  = 3
)

// HKState is what a device looks like to HomeKit. Nil pointers mean
// "leave the characteristic as it is".
type HKState struct {
	Active       int
	Current      int
	Target       *int
	HeatThresh   *float64
	CoolThresh   *float64
	CurrentTemp  *float64
	Speed        *float64 // Rotation Speed 20..100; nil when the fan is on auto or unknown
	Swing        int      // 0 off, 1 swinging
	Dry, FanOnly bool     // the Dry and Fan only switches
}

// fanSteps returns the manual fan speeds of a unit with n speeds, slowest
// first (auto has no place on the slider): Office's five, House's three.
func fanSteps(n int) []string {
	if n == 3 {
		return []string{"quiet", "low", "powerful"}
	}
	return []string{"superQuiet", "quiet", "low", "powerful", "superPowerful"}
}

// FanSteps is how many manual speeds there are for a unit with n speeds.
func fanCount(n int) int { return len(fanSteps(n)) }

// FanStep is the Rotation Speed distance between two speeds: the speeds are
// spread over 20..100 (20 apart for five, 40 for three).
func fanStep(n int) float64 { return 80 / float64(fanCount(n)-1) }

// SpeedFromFan maps a fan speed name to Rotation Speed for a unit with n
// speeds, nil for auto/unknown.
func SpeedFromFan(name string, n int) *float64 {
	for i, s := range fanSteps(n) {
		if s == name {
			v := 20 + fanStep(n)*float64(i)
			return &v
		}
	}
	return nil
}

// FanFromSpeed maps a Rotation Speed to the nearest fan speed name.
func FanFromSpeed(v float64, n int) string {
	steps := fanSteps(n)
	i := int(math.Round((v - 20) / fanStep(n)))
	return steps[min(max(i, 0), len(steps)-1)]
}

// FromDevice maps a device to HomeKit values. Target is only set to a value
// in allowedTargets.
func FromDevice(d kumo.Device, allowedTargets []int) HKState {
	st := HKState{HeatThresh: d.SpHeat, CoolThresh: d.SpCool, CurrentTemp: d.RoomTemp}
	if d.AirDirection == "swing" {
		st.Swing = 1
	}
	if d.Power == 0 || d.OperationMode == "off" {
		return st
	}
	st.Active = 1
	st.Dry, st.FanOnly = d.OperationMode == "dry", d.OperationMode == "vent"
	target := -1
	switch d.OperationMode {
	case "heat":
		st.Current, target = CurrentHeating, TargetHeat
	case "cool":
		st.Current, target = CurrentCooling, TargetCool
	case "auto":
		st.Current, target = CurrentIdle, TargetAuto
	case "autoHeat":
		st.Current, target = CurrentHeating, TargetAuto
	case "autoCool":
		st.Current, target = CurrentCooling, TargetAuto
	default: // vent, dry, anything unknown
		st.Current = CurrentIdle
	}
	if target >= 0 && slices.Contains(allowedTargets, target) {
		st.Target = &target
	}
	return st
}
