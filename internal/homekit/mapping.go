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

// fanSteps are the manual fan speeds, slowest first. Rotation Speed is
// 20 * (index+1); auto has no place on the slider.
var fanSteps = []string{"superQuiet", "quiet", "low", "powerful", "superPowerful"}

// SpeedFromFan maps a fan speed name to Rotation Speed, nil for auto/unknown.
func SpeedFromFan(name string) *float64 {
	for i, s := range fanSteps {
		if s == name {
			v := float64(20 * (i + 1))
			return &v
		}
	}
	return nil
}

// FanFromSpeed maps a Rotation Speed to the nearest fan speed name.
func FanFromSpeed(v float64) string {
	i := int(math.Round(v/20)) - 1
	return fanSteps[min(max(i, 0), len(fanSteps)-1)]
}

// FromDevice maps a device to HomeKit values. Target is only set to a value
// in allowedTargets.
func FromDevice(d kumo.Device, allowedTargets []int) HKState {
	st := HKState{HeatThresh: d.SpHeat, CoolThresh: d.SpCool, CurrentTemp: d.RoomTemp, Speed: SpeedFromFan(d.FanSpeed)}
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
