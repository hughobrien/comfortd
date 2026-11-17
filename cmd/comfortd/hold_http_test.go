// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

func holdPost(d deps, slug, form string, hdr map[string]string) *httptest.ResponseRecorder {
	return post(newMux(d), "/ui/units/"+slug+"/hold", formType, form, hdr)
}

func TestHoldUntilTime(t *testing.T) {
	srv, d := ctlDeps(t)
	rec := holdPost(d, "office", "mode=until&at=23:15", dsHdr)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `class="hold-msg err"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	w := srv.HoldWrites()
	sn, _ := d.Store.Get("office")
	want, _ := ui.HoldTimeFromClock(sn, d.Now(), "23:15")
	if len(w) != 1 || w[0].Zone != kumotest.OfficeZoneID || !w[0].Enabled || w[0].EndTime == nil || *w[0].EndTime != want.Unix() {
		t.Fatalf("writes %+v want %d", w, want.Unix())
	}
}

func TestHoldNextEvent(t *testing.T) {
	srv, d := ctlDeps(t)
	if rec := holdPost(d, "office", "mode=next", dsHdr); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	sn, _ := d.Store.Get("office")
	next := ui.BuildSchedule(sn, d.Now()).NextAt
	w := srv.HoldWrites()
	if next.IsZero() || len(w) != 1 || w[0].EndTime == nil || *w[0].EndTime != next.Unix() {
		t.Fatalf("writes %+v next %v", w, next)
	}
}

func TestHoldPermanentAndEnd(t *testing.T) {
	srv, d := ctlDeps(t)
	holdPost(d, "office", "mode=permanent", dsHdr)
	holdPost(d, "office", "mode=none", dsHdr)
	w := srv.HoldWrites()
	if len(w) != 2 || !w[0].Permanent || w[1].Enabled {
		t.Fatalf("writes %+v", w)
	}
}

func TestHoldNeedsDatastarHeader(t *testing.T) {
	srv, d := ctlDeps(t)
	if rec := holdPost(d, "office", "mode=permanent", nil); rec.Code != 403 || len(srv.HoldWrites()) != 0 {
		t.Fatalf("%d %+v", rec.Code, srv.HoldWrites())
	}
}

func TestHoldUnwritableUnit(t *testing.T) {
	srv, d := ctlDeps(t)
	if rec := holdPost(d, "house", "mode=permanent", dsHdr); rec.Code != 403 || len(srv.HoldWrites()) != 0 {
		t.Fatalf("%d %+v", rec.Code, srv.HoldWrites())
	}
}

func TestHoldBadInput(t *testing.T) {
	srv, d := ctlDeps(t)
	for _, form := range []string{"mode=until&at=99:99", "mode=until", "mode=sideways", ""} {
		rec := holdPost(d, "office", form, dsHdr)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hold-msg err") {
			t.Fatalf("%q: %d %s", form, rec.Code, rec.Body)
		}
	}
	if len(srv.HoldWrites()) != 0 {
		t.Fatalf("wrote %+v", srv.HoldWrites())
	}
}

func TestHoldCloudError(t *testing.T) {
	srv, d := ctlDeps(t)
	srv.FailNext("/v4/sites/"+kumotest.SiteID+"/hold", 500, 1)
	rec := holdPost(d, "office", "mode=permanent", dsHdr)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hold-msg err") || len(srv.HoldWrites()) != 0 {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body, srv.HoldWrites())
	}
}

func TestHoldRefreshesTheZone(t *testing.T) {
	_, d := ctlDeps(t)
	if rec := holdPost(d, "office", "mode=permanent", dsHdr); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	sn, _ := d.Store.Get("office")
	if sn.Zone == nil || !sn.Zone.HoldEnabled || sn.Zone.HoldType != "permanent" {
		t.Fatalf("zone not refreshed after the write: %+v", sn.Zone)
	}
}
