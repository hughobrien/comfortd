// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func TestSetLimitsAgainstTheFake(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	raw, err := c.Get(ctx, "/devices/"+kumotest.HouseSerial+"/status")
	if err != nil {
		t.Fatal(err)
	}
	var before struct{ Min, Max float64 }
	json.Unmarshal([]byte(strings.NewReplacer("minSetPoint", "min", "maxSetPoint", "max").Replace(string(raw))), &before)
	if err := c.SetLimits(ctx, kumotest.HouseSerial, 19, 24); err != nil {
		t.Fatal(err)
	}
	raw, err = c.Get(ctx, "/devices/"+kumotest.HouseSerial+"/status")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Min float64 `json:"minSetPoint"`
		Max float64 `json:"maxSetPoint"`
	}
	if err := json.Unmarshal(raw, &after); err != nil || after.Min != 19 || after.Max != 24 {
		t.Fatalf("status after = %s (%v)", raw, err)
	}
	if got := e.srv.LimitWrites(); len(got) != 1 || got[0].Serial != kumotest.HouseSerial || got[0].MinCool != 19 || got[0].MaxHeat != 24 {
		t.Fatalf("writes = %+v", got)
	}
	// the other unit is untouched
	raw, _ = c.Get(ctx, "/devices/"+kumotest.OfficeSerial+"/status")
	var off struct {
		Min float64 `json:"minSetPoint"`
	}
	json.Unmarshal(raw, &off)
	if off.Min == 19 {
		t.Fatalf("office status changed: %s", raw)
	}
}

func TestSetLimitsReportsFailure(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	e.srv.FailNext("/devices/"+kumotest.HouseSerial+"/relay-command", 500, 1)
	if err := e.client().SetLimits(context.Background(), kumotest.HouseSerial, 19, 24); err == nil {
		t.Fatal("want an error from a 500")
	}
}

func TestMinMaxPreference(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	if on, err := c.MinMaxEnabled(ctx); err != nil || on {
		t.Fatalf("start: %v %v", on, err)
	}
	if err := c.SetMinMaxEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if on, err := c.MinMaxEnabled(ctx); err != nil || !on {
		t.Fatalf("after: %v %v", on, err)
	}
	p, _ := c.Preferences(ctx)
	if p["celsius"] != true || p["appWalkthroughs"] == nil { // the rest of the object is sent back untouched
		t.Fatalf("other preferences lost: %v", p)
	}
	if _, n := e.srv.MinMaxEnabled(); n != 1 {
		t.Fatalf("writes %d", n)
	}
}
