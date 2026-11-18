// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"testing"
	"time"
)

func TestZoneAdapterHoldEnd(t *testing.T) {
	raw := []byte(`[{"id":"z","name":"n","adapter":{"deviceSerial":"S","scheduleHoldEndTime":1794969945},"holdMode":{"enabled":false,"holdType":"permanent","endTime":"2026-11-18T01:33:46.000Z"}}]`)
	zs, err := ParseZones(raw)
	if err != nil || len(zs) != 1 {
		t.Fatal(err)
	}
	if got := zs[0].AdapterHoldEnd.UTC().Format(time.RFC3339); got != "2026-11-18T02:45:45Z" {
		t.Fatalf("adapter hold end %s", got)
	}
	before := time.Date(2026, 11, 18, 1, 41, 0, 0, time.UTC)
	if z, th := zs[0].WithAdapterHold(before); !th || !z.HoldEnabled || z.HoldType != "until_specific_time" {
		t.Fatalf("%+v %v", z, th)
	}
	if _, th := zs[0].WithAdapterHold(before.Add(2 * time.Hour)); th {
		t.Fatal("past end time counted as a hold")
	}
}
