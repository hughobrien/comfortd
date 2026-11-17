// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import "testing"

func TestParsePacket(t *testing.T) {
	cases := []struct {
		in    string
		eio   byte
		sio   byte
		event string
		args  int
		data  string
	}{
		{`0{"sid":"abc","pingInterval":25000,"pingTimeout":20000}`, '0', 0, "", 0, `{"sid":"abc","pingInterval":25000,"pingTimeout":20000}`},
		{`2`, '2', 0, "", 0, ""},
		{`3`, '3', 0, "", 0, ""},
		{`40`, '4', '0', "", 0, ""},
		{`40{"sid":"s1"}`, '4', '0', "", 0, `{"sid":"s1"}`},
		{`44{"message":"nope"}`, '4', '4', "", 0, `{"message":"nope"}`},
		{`42["device_update",{"a":1}]`, '4', '2', "device_update", 1, ""},
		{`42["subscribed","SER"]`, '4', '2', "subscribed", 1, ""},
		{`42["hold_update"]`, '4', '2', "hold_update", 0, ""},
		{`42/ns,["x",1]`, '4', '2', "x", 1, ""},
		{`4217["with_ack",1]`, '4', '2', "with_ack", 1, ""},
		{`41`, '4', '1', "", 0, ""},
	}
	for _, c := range cases {
		p, err := parsePacket(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if p.Eio != c.eio || p.Sio != c.sio || p.Event != c.event || len(p.Args) != c.args {
			t.Errorf("%q: got eio=%c sio=%c event=%q args=%d", c.in, p.Eio, p.Sio, p.Event, len(p.Args))
		}
		if c.data != "" && string(p.Data) != c.data {
			t.Errorf("%q: data = %s", c.in, p.Data)
		}
	}
}

func TestParsePacketErrors(t *testing.T) {
	for _, in := range []string{"", "9", "42", `42{"not":"an array"}`, `42[]`, `42[1,2]`} {
		if _, err := parsePacket(in); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

func TestEncode(t *testing.T) {
	if got := encodePong(); got != "3" {
		t.Errorf("pong = %q", got)
	}
	if got := encodeConnect(); got != "40" {
		t.Errorf("connect = %q", got)
	}
	got, err := encodeEvent("subscribe", "SER1")
	if err != nil || got != `42["subscribe","SER1"]` {
		t.Errorf("event = %q, %v", got, err)
	}
}
