// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

func officeSnap(t *testing.T) store.Snapshot {
	t.Helper()
	raw, err := os.ReadFile("../../../pkg/kumo/kumotest/fixtures/device_office.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	return store.Snapshot{
		Unit:         store.Unit{Slug: "office", Name: "Office", Serial: "S1"},
		Raw:          map[string]json.RawMessage{"device": raw},
		At:           map[string]time.Time{"device": now},
		Errors:       map[string]string{"sensor": "404"},
		LastDeviceOK: now,
	}
}

func findRow(v UnitView, ep, path string) (Row, bool) {
	for _, e := range v.Endpoints {
		if e.Name != ep {
			continue
		}
		for _, r := range e.Rows {
			if r.Path == path {
				return r, true
			}
		}
	}
	return Row{}, false
}

func TestBuildUnitViewRows(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 30, 0, time.UTC)
	v := BuildUnitView(officeSnap(t), func(ep string) map[string]bool {
		if ep == "device" {
			return map[string]bool{"spHeat": true}
		}
		return nil
	}, now, time.Minute)
	r, ok := findRow(v, "device", "operationMode")
	if !ok || r.Value != `"heat"` {
		t.Fatalf("operationMode: %+v %v", r, ok)
	}
	if r, _ := findRow(v, "device", "updatedAt"); !r.Noisy {
		t.Error("updatedAt should be noisy")
	}
	if r, _ := findRow(v, "device", "spHeat"); !r.Changed {
		t.Error("spHeat should be changed")
	}
	if r, _ := findRow(v, "device", "spCool"); r.Changed {
		t.Error("spCool should not be changed")
	}
	if r, _ := findRow(v, "device", "model.gallery.imageUrl"); r.Path == "" {
		t.Error("nested rows missing")
	}
	// endpoints with errors but no data still appear
	var names []string
	for _, e := range v.Endpoints {
		names = append(names, e.Name)
	}
	if len(names) != 2 || names[0] != "device" || names[1] != "sensor" || v.Endpoints[1].Error != "404" {
		t.Fatalf("endpoints %v", v.Endpoints)
	}
	if v.Stale || v.Endpoints[0].At != time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC).Local().Format("15:04:05") {
		t.Fatalf("stale=%v at=%q", v.Stale, v.Endpoints[0].At)
	}
}

func TestNoisyArrayPath(t *testing.T) {
	if !isNoisy("a[0].rssi") || isNoisy("rssiX") {
		t.Fatal("noisy matching wrong")
	}
}
