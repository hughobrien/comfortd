// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

func TestFanHint(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name string
		d    *kumo.Device
		want bool
	}{
		{"nil", nil, false},
		{"off", &kumo.Device{Power: 0, OperationMode: "heat", SpHeat: p(20), RoomTemp: p(22)}, false},
		{"hot adjust", &kumo.Device{Power: 1, OperationMode: "heat", Display: kumo.DisplayConfig{HotAdjust: true}}, true},
		{"standby alone says nothing", &kumo.Device{Power: 1, OperationMode: "cool", Display: kumo.DisplayConfig{Standby: true}}, false},
		{"heat, room at setpoint", &kumo.Device{Power: 1, OperationMode: "heat", SpHeat: p(20), RoomTemp: p(20)}, true},
		{"heat, room above setpoint", &kumo.Device{Power: 1, OperationMode: "heat", SpHeat: p(20), RoomTemp: p(21.5)}, true},
		{"heat, room below setpoint", &kumo.Device{Power: 1, OperationMode: "heat", SpHeat: p(20), RoomTemp: p(18)}, false},
		{"cool, room above heat setpoint", &kumo.Device{Power: 1, OperationMode: "cool", SpHeat: p(20), RoomTemp: p(25)}, false},
		{"vent", &kumo.Device{Power: 1, OperationMode: "vent", SpHeat: p(20), RoomTemp: p(25)}, false},
	} {
		if got := fanHint(tc.d) != ""; got != tc.want {
			t.Errorf("%s: hint=%q want hint=%v", tc.name, fanHint(tc.d), tc.want)
		}
	}
}
