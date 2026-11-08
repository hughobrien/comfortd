// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func setup(t *testing.T) (*kumotest.Server, *kumo.Client, string) {
	t.Helper()
	srv := kumotest.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "credentials")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	c := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds,
		Sleep: func(context.Context, time.Duration) error { return nil }})
	return srv, c, filepath.Join(dir, "discovery.json")
}

func discovered(t *testing.T, c *kumo.Client, cache string) []store.Unit {
	t.Helper()
	units, err := Discover(context.Background(), c, cache)
	if err != nil {
		t.Fatal(err)
	}
	return units
}

func TestDiscoverLiveWritesCache(t *testing.T) {
	_, c, cache := setup(t)
	units := discovered(t, c, cache)
	if len(units) != 2 {
		t.Fatalf("units = %+v", units)
	}
	m := map[string]string{}
	for _, u := range units {
		m[u.Slug] = u.Serial
	}
	if m["office"] != kumotest.OfficeSerial || m["house"] != kumotest.HouseSerial {
		t.Fatalf("units = %+v", units)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal(err)
	}
}

type failAPI struct{}

func (failAPI) Get(context.Context, string) (json.RawMessage, error) { return nil, errors.New("down") }

func TestDiscoverFallsBackToCache(t *testing.T) {
	_, c, cache := setup(t)
	if _, err := Discover(context.Background(), failAPI{}, cache); err == nil {
		t.Fatal("want error with no cache")
	}
	discovered(t, c, cache)
	units, err := Discover(context.Background(), failAPI{}, cache)
	if err != nil || len(units) != 2 {
		t.Fatalf("units=%+v err=%v", units, err)
	}
}

func newPoller(t *testing.T) (*kumotest.Server, *store.Store, *Poller) {
	srv, c, cache := setup(t)
	st := store.New(discovered(t, c, cache))
	return srv, st, New(c, st, time.Minute)
}

func TestPollOnceFillsStore(t *testing.T) {
	srv, st, p := newPoller(t)
	p.PollOnce(context.Background())
	for _, sn := range st.All() {
		if sn.Raw[store.EndpointDevice] == nil || sn.Raw[store.EndpointZone] == nil {
			t.Fatalf("%s missing raw: %v", sn.Unit.Slug, sn.Errors)
		}
	}
	o, _ := st.Get("office")
	var z struct{ Name string }
	json.Unmarshal(o.Raw[store.EndpointZone], &z)
	if z.Name != "Office" {
		t.Fatalf("zone name = %q", z.Name)
	}
	// Discovery fetched zones once too, so count only the poll's request.
	if n := srv.Requests("/sites/" + kumotest.SiteID + "/zones"); n != 2 {
		t.Fatalf("zones requests = %d, want 2 (1 discovery + 1 poll)", n)
	}
}

func TestPollSeesExternalChange(t *testing.T) {
	srv, st, p := newPoller(t)
	srv.SetField(kumotest.OfficeSerial, "spHeat", 23)
	p.PollOnce(context.Background())
	o, _ := st.Get("office")
	if *o.Device.SpHeat != 23 {
		t.Fatalf("spHeat = %v", *o.Device.SpHeat)
	}
}

func TestPollErrorRecorded(t *testing.T) {
	srv, st, p := newPoller(t)
	ctx := context.Background()
	p.PollOnce(ctx)
	srv.FailNext("/devices/"+kumotest.OfficeSerial, 500, 1)
	p.PollOnce(ctx)
	o, _ := st.Get("office")
	if o.Errors[store.EndpointDevice] == "" || o.Raw[store.EndpointDevice] == nil {
		t.Fatalf("errors=%v raw present=%v", o.Errors, o.Raw[store.EndpointDevice] != nil)
	}
	p.PollOnce(ctx)
	o, _ = st.Get("office")
	if o.Errors[store.EndpointDevice] != "" {
		t.Fatal("error should clear on success")
	}
}

func TestSensorNotFound(t *testing.T) {
	_, st, p := newPoller(t)
	p.SensorUnits("office")
	p.PollSensor(context.Background())
	o, _ := st.Get("office")
	if o.Errors[store.EndpointSensor] != "not found" {
		t.Fatalf("errors = %v", o.Errors)
	}
}

func TestSensorPolledOnlyWhenListed(t *testing.T) {
	srv, st, p := newPoller(t)
	p.PollSensor(context.Background()) // default: none
	if n := srv.Requests("/devices/"+kumotest.OfficeSerial+"/sensor") + srv.Requests("/devices/"+kumotest.HouseSerial+"/sensor"); n != 0 {
		t.Fatalf("polled %d times by default", n)
	}
	if o, _ := st.Get("office"); o.Errors[store.EndpointSensor] != "" {
		t.Fatalf("errors = %v", o.Errors)
	}
	p.SensorUnits("house")
	p.PollSensor(context.Background())
	if n := srv.Requests("/devices/" + kumotest.HouseSerial + "/sensor"); n != 1 {
		t.Fatalf("house polled %d times, want 1", n)
	}
	if n := srv.Requests("/devices/" + kumotest.OfficeSerial + "/sensor"); n != 0 {
		t.Fatalf("office polled %d times, want 0", n)
	}
}

func TestKickPollsSoon(t *testing.T) {
	srv, _, p := newPoller(t)
	p.kickDelays = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	path := "/devices/" + kumotest.OfficeSerial
	before := srv.Requests(path)
	p.Kick("office")
	deadline := time.Now().Add(2 * time.Second)
	for srv.Requests(path) < before+3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.Requests(path) - before; got != 3 {
		t.Fatalf("kick polls = %d, want 3", got)
	}
}

func TestPollScheduleStoresEachUnitsEvents(t *testing.T) {
	_, st, p := newPoller(t)
	p.PollSchedule(context.Background())
	for slug, want := range map[string]int{"office": 4, "house": 3} {
		sn, _ := st.Get(slug)
		if e := sn.Errors[store.EndpointSchedule]; e != "" {
			t.Fatalf("%s: %s", slug, e)
		}
		s, err := kumo.ParseSchedule(sn.Raw[store.EndpointSchedule])
		if err != nil || s.Season != "Winter" || len(s.Events) != want {
			t.Fatalf("%s: %+v %v", slug, s, err)
		}
	}
}
