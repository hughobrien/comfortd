// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

type fakeSnaps map[string]*store.Snapshot

func (s fakeSnaps) get(slug string) (store.Snapshot, bool) {
	sn, ok := s[slug]
	if !ok {
		return store.Snapshot{}, false
	}
	return *sn, true
}

func officeSnap() *store.Snapshot {
	return &store.Snapshot{
		Device:       &kumo.Device{Serial: "S1", ModelNumber: "M1", Power: 1, OperationMode: "heat", SpHeat: f(19), RoomTemp: f(20.5), Connected: true},
		Profile:      &kumo.Profile{Min: map[string]float64{"heat": 10}, Max: map[string]float64{"heat": 31}},
		LastDeviceOK: now,
	}
}

func newBridge(t *testing.T, snaps fakeSnaps, force map[string]bool) *Bridge {
	t.Helper()
	b, err := New(Options{
		StateDir: t.TempDir(), Version: "test", Snapshot: snaps.get, Interval: time.Minute,
		Now:        func() time.Time { return now },
		ForceStale: force,
		Units: []store.Unit{
			{Slug: "office", Name: "Office", Serial: "S1"},
			{Slug: "house", Name: "House", Serial: "S2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBridgeBuilds(t *testing.T) {
	b := newBridge(t, fakeSnaps{"office": officeSnap()}, nil)
	ua := b.by["office"]
	if ua.acc.Id != 2 || b.by["house"].acc.Id != 3 {
		t.Fatalf("aids %d %d", ua.acc.Id, b.by["house"].acc.Id)
	}
	if ua.hc.CurrentTemperature.Value() != 20.5 || ua.thresh.Value() != 19 || ua.hc.Active.Value() != 1 || ua.hc.CurrentHeaterCoolerState.Value() != CurrentHeating {
		t.Fatalf("temp %v thresh %v active %v cur %v", ua.hc.CurrentTemperature.Value(), ua.thresh.Value(), ua.hc.Active.Value(), ua.hc.CurrentHeaterCoolerState.Value())
	}
	if b.Pin == "" {
		t.Fatal("no pin")
	}
}

func TestThresholdRangeFromProfile(t *testing.T) {
	sn := officeSnap()
	sn.Device.SpHeat = f(28)
	b := newBridge(t, fakeSnaps{"office": sn}, nil)
	th := b.by["office"].thresh
	if th.MinValue() != 10 || th.MaxValue() != 31 || th.StepValue() != 0.5 || th.Value() != 28 {
		t.Fatalf("min %v max %v step %v val %v", th.MinValue(), th.MaxValue(), th.StepValue(), th.Value())
	}
}

func TestThresholdOutsideProfileStillShown(t *testing.T) {
	sn := officeSnap()
	sn.Device.SpHeat = f(33)
	b := newBridge(t, fakeSnaps{"office": sn}, nil)
	if v := b.by["office"].thresh.Value(); v != 33 {
		t.Fatalf("got %v", v)
	}
}

func TestBridgeBuildsWithoutSnapshot(t *testing.T) {
	b := newBridge(t, fakeSnaps{}, nil)
	acc := b.Accessories()
	if len(acc) != 2 || !acc[0].Stale || acc[0].Slug != "office" {
		t.Fatalf("got %+v", acc)
	}
}

func TestSyncUpdates(t *testing.T) {
	sn := officeSnap()
	b := newBridge(t, fakeSnaps{"office": sn}, nil)
	sn.Device.SpHeat = f(21)
	b.Sync("office")
	if v := b.by["office"].thresh.Value(); v != 21 {
		t.Fatalf("got %v", v)
	}
}

func TestRemoteWritesRejected(t *testing.T) {
	b := newBridge(t, fakeSnaps{"office": officeSnap()}, nil)
	if b.onSetActive("office", 0) != ErrReadOnly || b.onSetTarget("office", 1) != ErrReadOnly || b.onSetHeatThresh("office", 20) != ErrReadOnly {
		t.Fatal("writes accepted")
	}
	// And they are wired up: a differing remote write is refused with -70402.
	hc := b.by["office"].hc
	if _, code := hc.Active.SetValueRequestFunc(0, nil); code != statusCommFailure {
		t.Fatalf("code %d", code)
	}
}

func TestStaleReadReturnsCommFailure(t *testing.T) {
	sn := officeSnap()
	b := newBridge(t, fakeSnaps{"office": sn}, nil)
	c := b.by["office"].hc.CurrentTemperature.C
	if _, code := c.ValueRequestFunc(nil); code != 0 {
		t.Fatalf("fresh: %d", code)
	}
	sn.LastDeviceOK = now.Add(-time.Hour)
	if _, code := c.ValueRequestFunc(nil); code != statusCommFailure {
		t.Fatalf("stale: %d", code)
	}
	b2 := newBridge(t, fakeSnaps{"office": officeSnap()}, map[string]bool{"office": true})
	if _, code := b2.by["office"].hc.Active.C.ValueRequestFunc(nil); code != statusCommFailure {
		t.Fatalf("forced: %d", code)
	}
}

func TestModesShowTargetAndCurrent(t *testing.T) {
	for mode, want := range map[string][2]int{
		"heat": {CurrentHeating, TargetHeat}, "cool": {CurrentCooling, TargetCool},
		"autoCool": {CurrentCooling, TargetAuto}, "autoHeat": {CurrentHeating, TargetAuto}, "auto": {CurrentIdle, TargetAuto},
	} {
		sn := officeSnap()
		sn.Device.OperationMode = mode
		b := newBridge(t, fakeSnaps{"office": sn}, nil)
		hc := b.by["office"].hc
		if hc.CurrentHeaterCoolerState.Value() != want[0] || hc.TargetHeaterCoolerState.Value() != want[1] {
			t.Errorf("%s: cur %v target %v, want %v", mode, hc.CurrentHeaterCoolerState.Value(), hc.TargetHeaterCoolerState.Value(), want)
		}
	}
}

func TestDryShowsActiveIdleKeepsTarget(t *testing.T) {
	sn := officeSnap()
	sn.Device.OperationMode = "dry"
	b := newBridge(t, fakeSnaps{"office": sn}, nil)
	hc := b.by["office"].hc
	if hc.Active.Value() != 1 || hc.CurrentHeaterCoolerState.Value() != CurrentIdle {
		t.Fatalf("active %v cur %v", hc.Active.Value(), hc.CurrentHeaterCoolerState.Value())
	}
}
