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
