// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"encoding/json"
	"fmt"
	"time"
)

type Site struct{ ID, Name string }

// WithAdapterHold returns the zone as it should be shown at now: a hold made on
// the wall thermostat (adapter scheduleHoldEndTime still in the future, holdMode
// off) counts as an "until a specific time" hold. thermostat is true then.
func (z Zone) WithAdapterHold(now time.Time) (out Zone, thermostat bool) {
	if !z.HoldEnabled && z.AdapterHoldEnd.After(now) {
		z.HoldEnabled, z.HoldType, z.HoldEnd = true, "until_specific_time", z.AdapterHoldEnd
		return z, true
	}
	return z, false
}

type Zone struct {
	ID, Name, Serial  string
	HasActiveSchedule bool
	HoldEnabled       bool
	HoldEnd           time.Time // when the hold lapses (the next schedule event); zero if unknown
	HoldType          string    // until_next_event, until_specific_time, permanent, unset; empty if unknown
	HoldKind          string    // holdMode.type: "hold" or "away" (the Comfort app's Away mode); empty if unknown
	// AdapterHoldEnd is the adapter's scheduleHoldEndTime: a hold made on the wall
	// thermostat (House's MHK2) sets it without turning holdMode on. Zero if none.
	AdapterHoldEnd time.Time
}

type Device struct {
	Serial, ModelNumber, OperationMode, FanSpeed, AirDirection, UpdatedAt string
	Power                                                                 int
	SpHeat, SpCool, RoomTemp, Humidity                                    *float64
	Connected                                                             bool
	ImageURL                                                              string
	TimeZone                                                              string // IANA name, e.g. America/Los_Angeles; schedule times are in it
	Display                                                               DisplayConfig
	RealValues                                                            map[string]any
}

// DisplayConfig holds the adapter's indicator flags. filter true = the unit
// wants its filter cleaned (observed). The others are expected to be true
// while defrosting, in standby, or hot-start adjusting (not yet observed).
type DisplayConfig struct{ Defrost, Filter, HotAdjust, Standby bool }

type Profile struct {
	Min, Max          map[string]float64
	NumberOfFanSpeeds int
	HasModeDry        bool
	HasModeHeat       bool
	HasModeVent       bool
	HasVaneDir        bool
	HasVaneSwing      bool
}

func (p Profile) SupportsCool() bool { _, ok := p.Max["cool"]; return ok }
func (p Profile) SupportsAuto() bool { _, ok := p.Max["auto"]; return ok }

func ParseSites(raw json.RawMessage) ([]Site, error) {
	var in []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("kumo: parse sites: %w", err)
	}
	out := make([]Site, len(in))
	for i, s := range in {
		out[i] = Site{ID: s.ID, Name: s.Name}
	}
	return out, nil
}

func ParseZones(raw json.RawMessage) ([]Zone, error) {
	var in []struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		HasActiveSchedule bool   `json:"hasActiveSchedule"`
		HoldMode          *struct {
			Enabled  bool   `json:"enabled"`
			HoldType string `json:"holdType"`
			Type     string `json:"type"`
			EndTime  string `json:"endTime"`
		} `json:"holdMode"`
		Adapter struct {
			DeviceSerial        string `json:"deviceSerial"`
			ScheduleHoldEndTime int64  `json:"scheduleHoldEndTime"`
		} `json:"adapter"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("kumo: parse zones: %w", err)
	}
	out := make([]Zone, len(in))
	for i, z := range in {
		out[i] = Zone{ID: z.ID, Name: z.Name, Serial: z.Adapter.DeviceSerial, HasActiveSchedule: z.HasActiveSchedule}
		if z.Adapter.ScheduleHoldEndTime > 0 {
			out[i].AdapterHoldEnd = time.Unix(z.Adapter.ScheduleHoldEndTime, 0).UTC()
		}
		if z.HoldMode != nil {
			out[i].HoldEnabled = z.HoldMode.Enabled
			out[i].HoldType = z.HoldMode.HoldType
			out[i].HoldKind = z.HoldMode.Type
			if t, err := time.Parse(time.RFC3339, z.HoldMode.EndTime); err == nil {
				out[i].HoldEnd = t
			}
			// An away has no holdType; "until ended" is an end time a hundred years out.
			if out[i].HoldType == "" && z.HoldMode.Type == "away" {
				out[i].HoldType = "until_specific_time"
				if out[i].HoldEnd.After(time.Now().AddDate(10, 0, 0)) {
					out[i].HoldType = "permanent"
				}
			}
		}
	}
	return out, nil
}

func ParseDevice(raw json.RawMessage) (Device, error) {
	var in struct {
		DeviceSerial  string         `json:"deviceSerial"`
		ModelNumber   string         `json:"modelNumber"`
		OperationMode string         `json:"operationMode"`
		FanSpeed      string         `json:"fanSpeed"`
		AirDirection  string         `json:"airDirection"`
		UpdatedAt     string         `json:"updatedAt"`
		Power         float64        `json:"power"`
		SpHeat        *float64       `json:"spHeat"`
		SpCool        *float64       `json:"spCool"`
		RoomTemp      *float64       `json:"roomTemp"`
		Humidity      *float64       `json:"humidity"`
		Connected     bool           `json:"connected"`
		TimeZone      string         `json:"timeZone"`
		RealValues    map[string]any `json:"realValues"`
		DisplayConfig DisplayConfig  `json:"displayConfig"`
		Model         struct {
			Gallery struct {
				ImageURL string `json:"imageUrl"`
			} `json:"gallery"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Device{}, fmt.Errorf("kumo: parse device: %w", err)
	}
	return Device{
		Serial: in.DeviceSerial, ModelNumber: in.ModelNumber, OperationMode: in.OperationMode,
		FanSpeed: in.FanSpeed, AirDirection: in.AirDirection, UpdatedAt: in.UpdatedAt,
		Power: int(in.Power), SpHeat: in.SpHeat, SpCool: in.SpCool, RoomTemp: in.RoomTemp,
		Humidity: in.Humidity, Connected: in.Connected, TimeZone: in.TimeZone, RealValues: in.RealValues,
		ImageURL: in.Model.Gallery.ImageURL, Display: in.DisplayConfig,
	}, nil
}

func ParseProfile(raw json.RawMessage) (Profile, error) {
	var in []struct {
		Max               map[string]float64 `json:"maximumSetPoints"`
		Min               map[string]float64 `json:"minimumSetPoints"`
		NumberOfFanSpeeds int                `json:"numberOfFanSpeeds"`
		HasModeDry        bool               `json:"hasModeDry"`
		HasModeHeat       bool               `json:"hasModeHeat"`
		HasModeVent       bool               `json:"hasModeVent"`
		HasVaneDir        bool               `json:"hasVaneDir"`
		HasVaneSwing      bool               `json:"hasVaneSwing"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Profile{}, fmt.Errorf("kumo: parse profile: %w", err)
	}
	if len(in) == 0 {
		return Profile{}, fmt.Errorf("kumo: parse profile: empty array")
	}
	p := in[0]
	return Profile{Min: p.Min, Max: p.Max, NumberOfFanSpeeds: p.NumberOfFanSpeeds,
		HasModeDry: p.HasModeDry, HasModeHeat: p.HasModeHeat, HasModeVent: p.HasModeVent,
		HasVaneDir: p.HasVaneDir, HasVaneSwing: p.HasVaneSwing}, nil
}
