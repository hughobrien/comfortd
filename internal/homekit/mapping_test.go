// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"testing"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

func ip(i int) *int { return &i }

func TestFromDevice(t *testing.T) {
	for _, c := range []struct {
		power   int
		mode    string
		allowed []int
		active  int
		current int
		target  *int
	}{
		{1, "heat", []int{1}, 1, 2, ip(1)},
		{0, "heat", []int{1}, 0, 0, nil},
		{0, "off", []int{1}, 0, 0, nil},
		{1, "vent", []int{1}, 1, 1, nil},
		{1, "dry", []int{1}, 1, 1, nil},
		{1, "cool", []int{1, 2}, 1, 3, ip(2)},
		{1, "auto", []int{0, 1, 2}, 1, 1, ip(0)},
		{1, "autoHeat", []int{0, 1}, 1, 2, ip(0)},
		{1, "autoCool", []int{0, 1, 2}, 1, 3, ip(0)},
	} {
		got := FromDevice(kumo.Device{Power: c.power, OperationMode: c.mode}, c.allowed)
		if got.Active != c.active || got.Current != c.current || (got.Target == nil) != (c.target == nil) ||
			(got.Target != nil && *got.Target != *c.target) {
			t.Errorf("%d/%s: got %+v", c.power, c.mode, got)
		}
	}
}

func TestMapCoolWhenOnlyHeatAllowed(t *testing.T) {
	got := FromDevice(kumo.Device{Power: 1, OperationMode: "cool"}, []int{1})
	if got.Current != CurrentCooling || got.Target != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestFromDeviceTemps(t *testing.T) {
	sp := 19.0
	got := FromDevice(kumo.Device{Power: 1, OperationMode: "heat", SpHeat: &sp}, []int{1})
	if got.HeatThresh == nil || *got.HeatThresh != 19 || got.CurrentTemp != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestFanSpeedMapping(t *testing.T) {
	for name, want := range map[string]float64{"superQuiet": 20, "quiet": 40, "low": 60, "powerful": 80, "superPowerful": 100} {
		if got := SpeedFromFan(name, 5); got == nil || *got != want {
			t.Errorf("%s: %v", name, got)
		}
		if FanFromSpeed(want, 5) != name {
			t.Errorf("%v -> %s", want, FanFromSpeed(want, 5))
		}
	}
	if SpeedFromFan("auto", 5) != nil || SpeedFromFan("", 5) != nil {
		t.Error("auto must not map to a speed")
	}
	for v, want := range map[float64]string{0: "superQuiet", 29: "superQuiet", 31: "quiet", 70: "powerful", 500: "superPowerful"} {
		if got := FanFromSpeed(v, 5); got != want {
			t.Errorf("%v -> %s want %s", v, got, want)
		}
	}
}

func TestFanSpeedMappingThree(t *testing.T) {
	for name, want := range map[string]float64{"quiet": 20, "low": 60, "powerful": 100} {
		if got := SpeedFromFan(name, 3); got == nil || *got != want {
			t.Errorf("%s: %v", name, got)
		}
		if FanFromSpeed(want, 3) != name {
			t.Errorf("%v -> %s", want, FanFromSpeed(want, 3))
		}
	}
	if SpeedFromFan("superQuiet", 3) != nil {
		t.Error("five-speed names are not House's")
	}
	for v, want := range map[float64]string{0: "quiet", 39: "quiet", 41: "low", 79: "low", 81: "powerful"} {
		if got := FanFromSpeed(v, 3); got != want {
			t.Errorf("%v -> %s want %s", v, got, want)
		}
	}
}

func TestFromDeviceModeSwitches(t *testing.T) {
	for mode, want := range map[string][2]bool{"dry": {true, false}, "vent": {false, true}, "heat": {false, false}, "off": {false, false}} {
		st := FromDevice(kumo.Device{OperationMode: mode, Power: 1}, allowedTargets)
		if st.Dry != want[0] || st.FanOnly != want[1] {
			t.Errorf("%s: dry=%v fan=%v", mode, st.Dry, st.FanOnly)
		}
	}
	if st := FromDevice(kumo.Device{OperationMode: "dry", Power: 0}, allowedTargets); st.Dry {
		t.Error("power off should clear the Dry switch")
	}
}
