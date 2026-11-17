// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../pkg/kumo/kumotest/fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Office": "office", "Living Room 2": "living-room-2", " House! ": "house"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPutParsesAndNotifies(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	var ups []Update
	st.Subscribe(func(u Update) { ups = append(ups, u) })
	raw := fixture(t, "device_office.json")
	t0 := time.Now()
	if err := st.Put("office", EndpointDevice, raw, t0); err != nil {
		t.Fatal(err)
	}
	sn, _ := st.Get("office")
	if sn.Device.OperationMode != "heat" {
		t.Fatalf("mode = %q", sn.Device.OperationMode)
	}
	if ups[0].Old != nil {
		t.Fatal("first update should have nil Old")
	}
	st.Put("office", EndpointDevice, raw, t0.Add(time.Second))
	if string(ups[1].Old) != string(raw) {
		t.Fatal("second update Old should equal first raw")
	}
}

func TestSubscribersInOrder(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	var got []int
	for i := 1; i <= 3; i++ {
		st.Subscribe(func(Update) { got = append(got, i) })
	}
	st.Put("office", EndpointDevice, fixture(t, "device_office.json"), time.Now())
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestStale(t *testing.T) {
	now := time.Now()
	iv := time.Minute
	st := New([]Unit{{Slug: "office"}})
	st.Put("office", EndpointDevice, fixture(t, "device_office.json"), now.Add(-150*time.Second))
	sn, _ := st.Get("office")
	if sn.Stale(now, iv) {
		t.Fatal("2.5 intervals should not be stale")
	}
	st.Put("office", EndpointDevice, fixture(t, "device_office.json"), now.Add(-210*time.Second))
	sn, _ = st.Get("office")
	if !sn.Stale(now, iv) {
		t.Fatal("3.5 intervals should be stale")
	}
	st.Put("office", EndpointDevice, []byte(`{"connected":false}`), now)
	sn, _ = st.Get("office")
	if !sn.Stale(now, iv) {
		t.Fatal("disconnected should be stale")
	}
}

func TestSetpointRangePerMode(t *testing.T) {
	prof := &kumo.Profile{
		Min: map[string]float64{"heat": 10, "cool": 19, "auto": 19},
		Max: map[string]float64{"heat": 28, "cool": 30, "auto": 28},
	}
	status := map[string]json.RawMessage{EndpointStatus: json.RawMessage(`{"minSetPoint":19.5,"maxSetPoint":28}`)}
	for _, tc := range []struct {
		name   string
		snap   Snapshot
		key    string
		lo, hi float64
		ok     bool
	}{
		{"profile heat, narrowed by status", Snapshot{Profile: prof, Raw: status}, "heat", 19.5, 28, true},
		{"profile heat alone", Snapshot{Profile: prof}, "heat", 10, 28, true},
		{"profile cool, narrowed by status", Snapshot{Profile: prof, Raw: status}, "cool", 19.5, 28, true},
		{"profile cool alone", Snapshot{Profile: prof}, "cool", 19, 30, true},
		{"profile auto, narrowed by status", Snapshot{Profile: prof, Raw: status}, "auto", 19.5, 28, true},
		{"profile without the key", Snapshot{Profile: &kumo.Profile{}, Raw: status}, "cool", 0, 0, false},
		{"status only", Snapshot{Raw: status}, "cool", 19.5, 28, true},
		{"nothing", Snapshot{}, "heat", 0, 0, false},
	} {
		lo, hi, ok := tc.snap.SetpointRange(tc.key)
		if lo != tc.lo || hi != tc.hi || ok != tc.ok {
			t.Errorf("%s: got %v–%v %v", tc.name, lo, hi, ok)
		}
	}
}

func patchSetMode(mode string) func(json.RawMessage) (json.RawMessage, error) {
	return func(old json.RawMessage) (json.RawMessage, error) {
		var m map[string]any
		if err := json.Unmarshal(old, &m); err != nil {
			return nil, err
		}
		m["operationMode"] = mode
		return json.Marshal(m)
	}
}

func TestPatchChangedNotifiesEveryone(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	st.Put("office", EndpointDevice, fixture(t, "device_office.json"), time.Now())
	var onChange, always int
	st.Subscribe(func(Update) { onChange++ })
	st.SubscribeAlways(func(Update) { always++ })
	changed, err := st.Patch("office", EndpointDevice, patchSetMode("cool"), time.Now())
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if onChange != 1 || always != 1 {
		t.Fatalf("onChange=%d always=%d", onChange, always)
	}
	sn, _ := st.Get("office")
	if sn.Device.OperationMode != "cool" {
		t.Fatalf("mode = %q", sn.Device.OperationMode)
	}
}

func TestPatchUnchangedAdvancesTimeAndNotifiesOnlyAlways(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	t0 := time.Now()
	st.Put("office", EndpointDevice, fixture(t, "device_office.json"), t0)
	var onChange, always int
	st.Subscribe(func(Update) { onChange++ })
	st.SubscribeAlways(func(Update) { always++ })
	same := func(old json.RawMessage) (json.RawMessage, error) {
		var m map[string]any
		json.Unmarshal(old, &m)
		return json.Marshal(m) // same content, different key order
	}
	t1 := t0.Add(time.Minute)
	changed, err := st.Patch("office", EndpointDevice, same, t1)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if onChange != 0 || always != 1 {
		t.Fatalf("onChange=%d always=%d", onChange, always)
	}
	sn, _ := st.Get("office")
	if !sn.At[EndpointDevice].Equal(t1) || !sn.LastDeviceOK.Equal(t1) {
		t.Fatalf("At=%v LastDeviceOK=%v, want %v", sn.At[EndpointDevice], sn.LastDeviceOK, t1)
	}
}

func TestPatchErrorLeavesStoreUntouched(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	raw := fixture(t, "device_office.json")
	st.Put("office", EndpointDevice, raw, time.Now())
	_, err := st.Patch("office", EndpointDevice, func(json.RawMessage) (json.RawMessage, error) {
		return nil, os.ErrInvalid
	}, time.Now())
	if err == nil {
		t.Fatal("want the fn error")
	}
	sn, _ := st.Get("office")
	if string(sn.Raw[EndpointDevice]) != string(raw) {
		t.Fatal("store changed on error")
	}
}

func TestPatchPassesNilOldWhenNothingStored(t *testing.T) {
	st := New([]Unit{{Slug: "office"}})
	var got json.RawMessage = json.RawMessage("x")
	_, err := st.Patch("office", EndpointZone, func(old json.RawMessage) (json.RawMessage, error) {
		got = old
		return nil, os.ErrNotExist // caller declines
	}, time.Now())
	if err == nil || got != nil {
		t.Fatalf("err=%v old=%v", err, got)
	}
}
