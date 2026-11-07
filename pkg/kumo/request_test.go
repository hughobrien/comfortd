// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func (e *env) clientWithSleeps(sleeps *[]time.Duration) *Client {
	c := e.client()
	c.o.Sleep = func(_ context.Context, d time.Duration) error { *sleeps = append(*sleeps, d); return nil }
	return c
}

func TestGetSites(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	raw, err := e.client().Get(context.Background(), "/sites/")
	if err != nil {
		t.Fatal(err)
	}
	sites, err := ParseSites(raw)
	if err != nil || len(sites) != 1 || sites[0].ID != kumotest.SiteID || sites[0].Name != "Home" {
		t.Fatalf("sites = %+v, %v", sites, err)
	}
}

func TestParseZonesFixture(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	raw, err := e.client().Get(context.Background(), "/sites/"+kumotest.SiteID+"/zones")
	if err != nil {
		t.Fatal(err)
	}
	zones, err := ParseZones(raw)
	if err != nil || len(zones) != 2 {
		t.Fatalf("zones = %+v, %v", zones, err)
	}
	names := map[string]Zone{}
	for _, z := range zones {
		names[z.Name] = z
	}
	o, ok := names["Office"]
	if !ok || names["House"].Name == "" {
		t.Fatalf("names = %v", names)
	}
	if o.Serial != kumotest.OfficeSerial || !o.HasActiveSchedule {
		t.Fatalf("office = %+v", o)
	}
}

func TestParseDeviceIntAndFloat(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	raw, _ := c.Get(ctx, "/devices/"+kumotest.OfficeSerial)
	o, err := ParseDevice(raw)
	if err != nil {
		t.Fatal(err)
	}
	if *o.RoomTemp != 20.5 || *o.SpHeat != 19 || o.RealValues["spHeat"] == nil || o.Humidity != nil || o.Serial != kumotest.OfficeSerial {
		t.Fatalf("office = %+v", o)
	}
	raw, _ = c.Get(ctx, "/devices/"+kumotest.HouseSerial)
	h, err := ParseDevice(raw)
	if err != nil {
		t.Fatal(err)
	}
	if *h.RoomTemp != 20 || *h.Humidity != 52 {
		t.Fatalf("house roomTemp=%v humidity=%v", *h.RoomTemp, h.Humidity)
	}
}

func TestParseProfileFixture(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	raw, _ := c.Get(ctx, "/devices/"+kumotest.OfficeSerial+"/profile")
	o, err := ParseProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if o.Min["heat"] != 10 || o.Max["heat"] != 31 || o.NumberOfFanSpeeds != 5 || !o.SupportsCool() {
		t.Fatalf("office = %+v", o)
	}
	raw, _ = c.Get(ctx, "/devices/"+kumotest.HouseSerial+"/profile")
	h, _ := ParseProfile(raw)
	if h.Max["heat"] != 28 {
		t.Fatalf("house = %+v", h)
	}
}

func TestGet404(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	_, err := e.client().Get(context.Background(), "/devices/"+kumotest.OfficeSerial+"/sensor")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGet401RefreshesAndRetries(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	if _, err := c.Get(ctx, "/sites/"); err != nil {
		t.Fatal(err)
	}
	e.srv.ExpireAccess()
	if _, err := c.Get(ctx, "/sites/"); err != nil {
		t.Fatal(err)
	}
	if e.srv.Refreshes() != 1 {
		t.Fatalf("Refreshes() = %d, want 1", e.srv.Refreshes())
	}
}

func TestGet429Backoff(t *testing.T) {
	path := "/devices/" + kumotest.OfficeSerial
	e := newEnv(t, kumotest.Password)
	var sleeps []time.Duration
	e.srv.FailNext(path, 429, 2)
	if _, err := e.clientWithSleeps(&sleeps).Get(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 2 || sleeps[0] != 2*time.Second || sleeps[1] != 4*time.Second {
		t.Fatalf("sleeps = %v", sleeps)
	}

	sleeps = nil
	e.srv.FailNext(path, 429, 4)
	_, err := e.clientWithSleeps(&sleeps).Get(context.Background(), path)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 429 {
		t.Fatalf("err = %v, want HTTPError 429", err)
	}
	if len(sleeps) != 3 || sleeps[2] != 8*time.Second {
		t.Fatalf("sleeps = %v", sleeps)
	}
}

func TestSendCommandFake(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	if err := e.client().SendCommand(context.Background(), kumotest.OfficeSerial, map[string]any{"spHeat": 21}); err != nil {
		t.Fatal(err)
	}
	cmds := e.srv.Commands()
	if len(cmds) != 1 || cmds[0].Serial != kumotest.OfficeSerial || cmds[0].Commands["spHeat"] != float64(21) {
		t.Fatalf("commands = %+v", cmds)
	}
}
