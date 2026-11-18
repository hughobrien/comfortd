// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

// fanHint says when the unit holds its indoor fan down whatever the fan
// setting. In heat the unit stops the indoor fan or runs it very low while the
// compressor is off or starting and the coil is cold ("cold air prevention" in
// the MSZ-FX service manual; Mitsubishi's name for the warm-up phase is "hot
// adjust", which is displayConfig.hotAdjust). That happens at the start of
// heating, when the thermostat goes from off to on, and after a defrost; with
// the room at the setpoint the compressor is off too. displayConfig.standby is
// not used: the CN105 protocol reference reads it as another unit with priority
// requesting a conflicting mode, so it is not an idle signal.
// It returns a short label for the card heading and the longer explanation for
// its tooltip.
func fanHint(d *kumo.Device) (label, why string) {
	if d == nil || d.Power != 1 {
		return "", ""
	}
	switch {
	case d.Display.HotAdjust:
		return "Warming up, fan low", "Warming up: the fan stays off or very low until the coil is warm."
	case control.ModeFamily(d.OperationMode) == "heat" && d.SpHeat != nil && d.RoomTemp != nil && *d.RoomTemp >= *d.SpHeat:
		return "Setpoint reached, fan low", "Room is at the setpoint: the fan may stay off or very low until heating restarts."
	}
	return "", ""
}
