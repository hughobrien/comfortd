// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func limitsPost(d deps, slug, form string, hdr map[string]string) (code int, body string) {
	rec := post(newMux(d), "/ui/units/"+slug+"/limits", formType, form, hdr)
	return rec.Code, rec.Body.String()
}

func TestLimitsSet(t *testing.T) {
	srv, d := ctlDepsFor(t, []string{"office", "house"})
	code, body := limitsPost(d, "house", "min=19&max=24", dsHdr)
	if code != 200 || strings.Contains(body, "err") {
		t.Fatalf("%d %s", code, body)
	}
	w := srv.LimitWrites()
	if len(w) != 1 || w[0].Serial != kumotest.HouseSerial || w[0].MinCool != 19 || w[0].MaxHeat != 24 {
		t.Fatalf("writes %+v", w)
	}
}

func TestLimitsRejects(t *testing.T) {
	srv, d := ctlDepsFor(t, []string{"office", "house"})
	for name, tc := range map[string]struct{ slug, form string }{
		"not a number":        {"house", "min=x&max=24"},
		"below the profile":   {"house", "min=10&max=24"},
		"floor above ceiling": {"house", "min=26&max=24"},
	} {
		code, body := limitsPost(d, tc.slug, tc.form, dsHdr)
		if code != 200 || !strings.Contains(body, `class="hold-msg err"`) {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if w := srv.LimitWrites(); len(w) != 0 {
		t.Fatalf("writes %+v", w)
	}
}

func TestLimitsNeedDatastarHeader(t *testing.T) {
	srv, d := ctlDepsFor(t, []string{"office", "house"})
	if code, _ := limitsPost(d, "house", "min=19&max=24", nil); code != 403 || len(srv.LimitWrites()) != 0 {
		t.Fatalf("%d %+v", code, srv.LimitWrites())
	}
}

func TestLimitsForbiddenOnReadOnlyUnit(t *testing.T) {
	srv, d := ctlDeps(t) // only office is writable
	if code, _ := limitsPost(d, "house", "min=19&max=24", dsHdr); code != 403 || len(srv.LimitWrites()) != 0 {
		t.Fatalf("%d %+v", code, srv.LimitWrites())
	}
}

func TestLimitsCheckboxTurnsTheFeatureOn(t *testing.T) {
	srv, d := ctlDepsFor(t, []string{"office", "house"})
	if on, _ := d.Ctl.LimitsOn(); on {
		t.Fatal("starts on")
	}
	// ticked: preference written, then limits
	if code, body := limitsPost(d, "house", "enabled=on&min=19&max=24", dsHdr); code != 200 || strings.Contains(body, "err") {
		t.Fatalf("%d %s", code, body)
	}
	if on, n := srv.MinMaxEnabled(); !on || n != 1 {
		t.Fatalf("preference on=%v writes=%d", on, n)
	}
	if on, known := d.Ctl.LimitsOn(); !on || !known {
		t.Fatal("controller did not learn the new value")
	}
	// same again: nothing more is written
	limitsPost(d, "house", "enabled=on&min=19&max=24", dsHdr)
	if _, n := srv.MinMaxEnabled(); n != 1 || len(srv.LimitWrites()) != 1 {
		t.Fatalf("repeat wrote again: prefs %d limits %d", n, len(srv.LimitWrites()))
	}
	// unticked: preference turned off
	limitsPost(d, "house", "min=19&max=24", dsHdr)
	if on, n := srv.MinMaxEnabled(); on || n != 2 {
		t.Fatalf("preference on=%v writes=%d", on, n)
	}
}

func TestLimitsDisabledSlidersOnlySaveTheCheckbox(t *testing.T) {
	srv, d := ctlDepsFor(t, []string{"office", "house"})
	limitsPost(d, "house", "enabled=on&min=19&max=24", dsHdr)
	// unticked: the browser sends no slider values
	if code, body := limitsPost(d, "house", "", dsHdr); code != 200 || strings.Contains(body, "err") {
		t.Fatalf("%d %s", code, body)
	}
	if on, n := srv.MinMaxEnabled(); on || n != 2 || len(srv.LimitWrites()) != 1 {
		t.Fatalf("on=%v prefWrites=%d limitWrites=%d", on, n, len(srv.LimitWrites()))
	}
}
