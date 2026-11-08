// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import "testing"

func TestRouteFoldsIds(t *testing.T) {
	for in, want := range map[string]string{
		"/devices/4Y34P009E101041F/status":                                    "/devices/{id}/status",
		"/v4/schedule-seasons/00000000-0000-4000-8000-000000000002/schedules": "/v4/schedule-seasons/{id}/schedules",
		"/sites/":               "/sites/",
		"/devices/send-command": "/devices/send-command",
	} {
		if got := Route(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
