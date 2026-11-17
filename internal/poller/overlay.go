// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"encoding/json"
	"errors"
	"time"
)

// errNothingStored means a push arrived before the first poll stored a body to
// overlay it on; the push is dropped and the poll fills the store.
var errNothingStored = errors.New("nothing stored to overlay on")

// pushFastKeys are the live fields a "fast" device_update carries that map
// straight onto the REST device body.
var pushFastKeys = []string{"rssi", "roomTemp", "operationMode", "power", "spCool", "spHeat", "airDirection", "fanSpeed", "connected"}

// OverlayDevice applies a device_update push onto the stored REST device body.
// Only keys the stored body already has are touched, so the stored key set stays
// the REST one and history sees only real changes. A "full" push overlays every
// such key; a "fast" one only the live fields (displayConfigFast maps onto
// displayConfig).
func OverlayDevice(old, push json.RawMessage) (json.RawMessage, error) {
	if len(old) == 0 {
		return nil, errNothingStored
	}
	var base, p map[string]json.RawMessage
	if err := json.Unmarshal(old, &base); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(push, &p); err != nil {
		return nil, err
	}
	var payloadType string
	_ = json.Unmarshal(p["payloadType"], &payloadType)
	if payloadType == "fast" {
		for _, k := range pushFastKeys {
			if v, ok := p[k]; ok {
				if _, has := base[k]; has {
					base[k] = v
				}
			}
		}
		if v, ok := p["displayConfigFast"]; ok {
			if _, has := base["displayConfig"]; has {
				base["displayConfig"] = v
			}
		}
	} else {
		for k, v := range p {
			if k == "payloadType" || k == "date" {
				continue
			}
			if _, has := base[k]; has {
				base[k] = v
			}
		}
	}
	return json.Marshal(base)
}

// endTimeSlack is how far apart two endTime stamps of the same hold may be
// before they count as different. The REST zone carries cloud-rounded stamps
// (…06:00:01.834Z), a push the exact boundary (…06:00:00.000Z).
const endTimeSlack = 5 * time.Second

// OverlayHold applies a hold_update push onto the stored REST zone body's
// holdMode. deviceSerial and date are dropped. If the pushed hold is the same
// one as the stored (same enabled/holdType/type) and its endTime is within
// endTimeSlack of the stored one, the stored endTime is kept.
func OverlayHold(old, push json.RawMessage) (json.RawMessage, error) {
	if len(old) == 0 {
		return nil, errNothingStored
	}
	var zone map[string]json.RawMessage
	if err := json.Unmarshal(old, &zone); err != nil {
		return nil, err
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(push, &p); err != nil {
		return nil, err
	}
	delete(p, "deviceSerial")
	delete(p, "date")
	var cur map[string]json.RawMessage
	_ = json.Unmarshal(zone["holdMode"], &cur)
	if cur != nil && sameHold(cur, p) {
		if keepEnd(cur["endTime"], p["endTime"]) {
			p["endTime"] = cur["endTime"]
		}
	}
	hm := map[string]json.RawMessage{}
	for k, v := range cur {
		hm[k] = v
	}
	for k, v := range p {
		hm[k] = v
	}
	b, err := json.Marshal(hm)
	if err != nil {
		return nil, err
	}
	zone["holdMode"] = b
	return json.Marshal(zone)
}

func sameHold(a, b map[string]json.RawMessage) bool {
	for _, k := range []string{"enabled", "type", "holdType"} {
		if string(a[k]) != string(b[k]) {
			return false
		}
	}
	return true
}

func keepEnd(stored, pushed json.RawMessage) bool {
	var a, b time.Time
	if json.Unmarshal(stored, &a) != nil || json.Unmarshal(pushed, &b) != nil {
		return false
	}
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= endTimeSlack
}
