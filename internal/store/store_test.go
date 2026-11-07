// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"testing"
	"time"
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
