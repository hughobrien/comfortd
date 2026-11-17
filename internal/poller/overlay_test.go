// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"encoding/json"
	"os"
	"testing"
)

func obj(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func restDevice(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../../pkg/kumo/kumotest/fixtures/device_office.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const pushFull = `{"id":"x","deviceSerial":"TESTSERIAL0001","rssi":-65,"power":1,"operationMode":"cool","humidity":48,
"scheduleOwner":"adapter","scheduleHoldEndTime":0,"fanSpeed":"quiet","airDirection":"auto","roomTemp":21,"spCool":25,"spHeat":19,
"spAuto":null,"connected":true,"previousOperationMode":"heat","updatedAt":"2026-11-17T22:15:26.382Z",
"displayConfig":{"filter":false,"defrost":true,"hotAdjust":false,"standby":false},"realValues":{},"payloadType":"full",
"date":"2026-11-17T22:15:26.552Z","extraPushOnly":1}`

const pushFast = `{"deviceSerial":"TESTSERIAL0001","rssi":-40,"roomTemp":22,"operationMode":"heat","power":1,"spCool":26,"spHeat":20,
"airDirection":"auto","fanSpeed":"auto","twoFiguresCode":"A0","unusualFigures":32768,"statusDisplay":0,"realValues":{},
"displayConfigFast":{"filter":true,"defrost":false,"hotAdjust":false,"standby":false},"payloadType":"fast","connected":true,
"date":"2026-11-17T22:15:26.942Z"}`

func TestOverlayDeviceFullKeepsRESTKeySet(t *testing.T) {
	old := restDevice(t)
	got, err := OverlayDevice(old, json.RawMessage(pushFull))
	if err != nil {
		t.Fatal(err)
	}
	m, o := obj(t, got), obj(t, old)
	if len(m) != len(o) {
		t.Fatalf("key set changed: %d keys, want %d", len(m), len(o))
	}
	for k := range m {
		if _, ok := o[k]; !ok {
			t.Errorf("push-only key %q leaked", k)
		}
	}
	if m["operationMode"] != "cool" || m["spCool"] != float64(25) || m["fanSpeed"] != "quiet" {
		t.Fatalf("values not applied: %v %v %v", m["operationMode"], m["spCool"], m["fanSpeed"])
	}
	if dc := m["displayConfig"].(map[string]any); dc["defrost"] != true {
		t.Fatalf("displayConfig not applied: %v", dc)
	}
}

func TestOverlayDeviceFastAppliesLiveFieldsOnly(t *testing.T) {
	old := restDevice(t)
	got, err := OverlayDevice(old, json.RawMessage(pushFast))
	if err != nil {
		t.Fatal(err)
	}
	m, o := obj(t, got), obj(t, old)
	if len(m) != len(o) {
		t.Fatalf("key set changed: %d keys, want %d", len(m), len(o))
	}
	if m["roomTemp"] != float64(22) || m["spHeat"] != float64(20) || m["rssi"] != float64(-40) {
		t.Fatalf("live fields not applied: %v", m)
	}
	if m["modelNumber"] != o["modelNumber"] || m["updatedAt"] != o["updatedAt"] {
		t.Fatal("non-live fields must be kept from the stored body")
	}
	if !equalJSON(m["displayConfig"], o["displayConfig"]) {
		t.Fatalf("displayConfigFast disagrees with the full displayConfig and must be ignored: %v", m["displayConfig"])
	}
}

func TestOverlayDeviceNeedsStoredBody(t *testing.T) {
	if _, err := OverlayDevice(nil, json.RawMessage(pushFast)); err != errNothingStored {
		t.Fatalf("err = %v, want errNothingStored", err)
	}
}

const restZone = `{"id":"z","name":"Office","holdMode":{"id":"h","enabled":false,"type":"hold","holdType":"until_next_event","origin":"adapter","endTime":"2026-11-07T06:00:01.834Z","spHeat":null,"spCool":null,"operationMode":null,"fanSpeed":null,"airDirection":null}}`

func TestOverlayHoldStartsAndKeepsZoneKeys(t *testing.T) {
	push := `{"deviceSerial":"TESTSERIAL0001","date":"2026-11-17T22:16:40.972Z","id":"h2","enabled":true,"type":"hold","holdType":"until_next_event","origin":"adapter","endTime":"2026-11-18T06:00:00.000Z","operationMode":null,"fanSpeed":null,"airDirection":null,"spCool":null,"spHeat":null}`
	got, err := OverlayHold(json.RawMessage(restZone), json.RawMessage(push))
	if err != nil {
		t.Fatal(err)
	}
	z := obj(t, got)
	if z["name"] != "Office" {
		t.Fatal("zone keys lost")
	}
	h := z["holdMode"].(map[string]any)
	if h["enabled"] != true || h["id"] != "h2" || h["endTime"] != "2026-11-18T06:00:00.000Z" {
		t.Fatalf("hold not applied: %v", h)
	}
	if _, ok := h["deviceSerial"]; ok {
		t.Fatal("deviceSerial leaked into holdMode")
	}
	if _, ok := h["date"]; ok {
		t.Fatal("date leaked into holdMode")
	}
}

func TestOverlayHoldKeepsStoredEndTimeWithinASecondOrTwo(t *testing.T) {
	// the REST zone carries a millisecond-precision endTime; an equal-hold push a
	// moment apart must not look like a change at the next poll
	stored := `{"id":"z","holdMode":{"id":"h","enabled":true,"type":"hold","holdType":"until_next_event","origin":"adapter","endTime":"2026-11-18T06:00:01.834Z"}}`
	push := `{"id":"h","enabled":true,"type":"hold","holdType":"until_next_event","origin":"adapter","endTime":"2026-11-18T06:00:00.000Z"}`
	got, err := OverlayHold(json.RawMessage(stored), json.RawMessage(push))
	if err != nil {
		t.Fatal(err)
	}
	h := obj(t, got)["holdMode"].(map[string]any)
	if h["endTime"] != "2026-11-18T06:00:01.834Z" {
		t.Fatalf("endTime = %v, want the stored one kept", h["endTime"])
	}
	// but a really different end time is applied
	push2 := `{"id":"h","enabled":true,"type":"hold","holdType":"until_specific_time","origin":"adapter","endTime":"2026-11-18T09:00:00.000Z"}`
	got, _ = OverlayHold(json.RawMessage(stored), json.RawMessage(push2))
	h = obj(t, got)["holdMode"].(map[string]any)
	if h["endTime"] != "2026-11-18T09:00:00.000Z" || h["holdType"] != "until_specific_time" {
		t.Fatalf("new hold not applied: %v", h)
	}
}

func TestOverlayHoldNeedsStoredZone(t *testing.T) {
	if _, err := OverlayHold(nil, json.RawMessage(`{"enabled":true}`)); err != errNothingStored {
		t.Fatalf("err = %v", err)
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
