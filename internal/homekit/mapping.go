// SPDX-License-Identifier: GPL-3.0-or-later

// Package homekit exposes units to Apple Home as HeaterCooler accessories.
package homekit

import (
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
	Active      int
	Current     int
	Target      *int
	HeatThresh  *float64
	CurrentTemp *float64
}

// FromDevice maps a device to HomeKit values. Target is only set to a value
// in allowedTargets.
func FromDevice(d kumo.Device, allowedTargets []int) HKState {
	st := HKState{HeatThresh: d.SpHeat, CurrentTemp: d.RoomTemp}
	if d.Power == 0 || d.OperationMode == "off" {
		return st
	}
	st.Active = 1
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
