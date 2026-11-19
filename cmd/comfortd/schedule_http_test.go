// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
)

var dsHdr = map[string]string{"Datastar-Request": "true"}

// officeForm is the editor's form for office's stored schedule, as the browser
// would submit it, with edit applied to the rows.
func officeForm(t *testing.T, d deps, edit func(rows []ui.EditRow) []ui.EditRow) string {
	t.Helper()
	sn, _ := d.Store.Get("office")
	rows := edit(ui.BuildScheduleEdit(sn))
	f := url.Values{}
	for _, r := range rows {
		f.Add("key", r.Key)
		f.Add("at", r.At)
		f.Add("mode", r.Mode)
		f.Add("sph", r.SpHeat)
		f.Add("spc", r.SpCool)
		f.Add("fan", r.Fan)
		f.Add("vane", r.Vane)
		for _, c := range ui.DayChips {
			if r.Days[c.Code] {
				f.Add("days-"+r.Key, c.Code)
			}
		}
	}
	return f.Encode()
}

const formType = "application/x-www-form-urlencoded"

func TestScheduleEditorShowsRows(t *testing.T) {
	_, d := ctlDeps(t)
	req := httptest.NewRequest("GET", "/ui/units/office/schedule/edit", nil)
	req.Header.Set("Datastar-Request", "true")
	rec := httptest.NewRecorder()
	newMux(d).ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Count(body, `name="at"`) != 4 || !strings.Contains(body, "Save") || !strings.Contains(body, "Cancel") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

func TestScheduleSaveMovesAnEvent(t *testing.T) {
	srv, d := ctlDeps(t)
	body := officeForm(t, d, func(rows []ui.EditRow) []ui.EditRow {
		for i := range rows {
			if rows[i].At == "02:00" {
				rows[i].At = "02:01"
			}
		}
		return rows
	})
	rec := post(newMux(d), "/ui/units/office/schedule", formType, body, dsHdr)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	w := srv.ScheduleWrites()
	if len(w) != 2 || w[0].Op != "add" || w[0].Event["startTime"] != "0201" || w[1].Op != "delete" || w[1].Event["startTime"] != "0200" {
		t.Fatalf("writes %+v", w)
	}
	if !strings.Contains(rec.Body.String(), "02:01") || strings.Contains(rec.Body.String(), "02:00") {
		t.Fatalf("response is not the refreshed table: %s", rec.Body)
	}
	// Saving the same thing again changes nothing.
	post(newMux(d), "/ui/units/office/schedule", formType, officeForm(t, d, func(r []ui.EditRow) []ui.EditRow { return r }), dsHdr)
	if n := len(srv.ScheduleWrites()); n != 2 {
		t.Fatalf("no-op save wrote: %d", n)
	}
}

func TestScheduleSaveAddAndDelete(t *testing.T) {
	srv, d := ctlDeps(t)
	body := officeForm(t, d, func(rows []ui.EditRow) []ui.EditRow {
		n := ui.NewEditRow("n1")
		n.At = "12:00"
		n.Mode = "cool"
		n.SpCool = "24"
		return append(rows[1:], n) // drops the first row, adds one
	})
	if rec := post(newMux(d), "/ui/units/office/schedule", formType, body, dsHdr); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	w := srv.ScheduleWrites()
	if len(w) != 2 || w[0].Op != "add" || w[0].Event["operationMode"] != "cool" || w[0].Event["spCool"] != 24.0 || w[1].Op != "delete" {
		t.Fatalf("writes %+v", w)
	}
}

func TestScheduleSaveRejectsBadRows(t *testing.T) {
	srv, d := ctlDeps(t)
	cases := map[string]func([]ui.EditRow) []ui.EditRow{
		"no days": func(r []ui.EditRow) []ui.EditRow { r[0].Days = nil; return r },
		"temp":    func(r []ui.EditRow) []ui.EditRow { r[0].SpHeat = "45"; return r },
		"auto": func(r []ui.EditRow) []ui.EditRow {
			r[0].Mode, r[0].SpHeat, r[0].SpCool = "auto", "22", "22.5"
			return r
		},
		"clash": func(r []ui.EditRow) []ui.EditRow {
			c := r[0]
			c.Key = "n2"
			return append(r, c)
		},
	}
	for name, edit := range cases {
		rec := post(newMux(d), "/ui/units/office/schedule", formType, officeForm(t, d, edit), dsHdr)
		if !strings.Contains(rec.Body.String(), `class="sched-msg err"`) {
			t.Errorf("%s: no error shown: %s", name, rec.Body)
		}
	}
	if n := len(srv.ScheduleWrites()); n != 0 {
		t.Fatalf("bad rows were written: %d", n)
	}
}

func TestScheduleEditRejected(t *testing.T) {
	_, d := ctlDeps(t)
	h := newMux(d)
	if rec := post(h, "/ui/units/office/schedule", formType, "key=x", nil); rec.Code != 403 {
		t.Fatalf("no datastar header: %d", rec.Code)
	}
	if rec := post(h, "/ui/units/nope/schedule", formType, "", dsHdr); rec.Code != 404 {
		t.Fatalf("unknown: %d", rec.Code)
	}
}
