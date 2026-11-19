// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/starfederation/datastar-go/datastar"
)

// registerSchedule adds the schedule editor's endpoints. They answer with
// datastar patches that replace the card's schedule body.
func (d deps) registerSchedule(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/units/{slug}/schedule/edit", d.handleScheduleEdit)
	mux.HandleFunc("GET /ui/units/{slug}/schedule/read", d.handleScheduleRead)
	mux.HandleFunc("GET /ui/units/{slug}/schedule/new-row", d.handleScheduleNewRow)
	mux.HandleFunc("POST /ui/units/{slug}/schedule", d.handleScheduleSave)
}

// scheduleGuard checks the request and returns the unit's snapshot.
func (d deps) scheduleGuard(w http.ResponseWriter, r *http.Request) (store.Snapshot, bool) {
	slug := r.PathValue("slug")
	if !uiOK(w, r) || !d.known(w, slug) {
		return store.Snapshot{}, false
	}
	sn, _ := d.Store.Get(slug)
	return sn, true
}

func (d deps) patchBody(sse *datastar.ServerSentEventGenerator, sn store.Snapshot) error {
	v := d.viewFor(sn, d.Now())
	return sse.PatchElementTempl(templates.ScheduleBody(v.Card),
		datastar.WithSelectorf("#sched-body-%s", sn.Unit.Slug), datastar.WithMode(datastar.ElementPatchModeReplace))
}

func (d deps) patchEditor(sse *datastar.ServerSentEventGenerator, sn store.Snapshot, msg string) error {
	return sse.PatchElementTempl(templates.ScheduleEditor(sn.Unit.Slug, ui.BuildSchedule(sn, d.Now()).Heading, ui.BuildScheduleEdit(sn), ui.BuildEditOpts(sn), msg),
		datastar.WithSelectorf("#sched-body-%s", sn.Unit.Slug), datastar.WithMode(datastar.ElementPatchModeReplace))
}

func (d deps) handleScheduleEdit(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.scheduleGuard(w, r)
	if !ok {
		return
	}
	d.patchEditor(datastar.NewSSE(w, r), sn, "")
}

func (d deps) handleScheduleRead(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.scheduleGuard(w, r)
	if !ok {
		return
	}
	d.patchBody(datastar.NewSSE(w, r), sn)
}

func (d deps) handleScheduleNewRow(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.scheduleGuard(w, r)
	if !ok {
		return
	}
	row := ui.NewEditRow(fmt.Sprintf("n%d", time.Now().UnixNano()))
	datastar.NewSSE(w, r).PatchElementTempl(templates.ScheduleEditRow(sn.Unit.Slug, row, ui.BuildEditOpts(sn)),
		datastar.WithSelectorf("#sched-rows-%s", sn.Unit.Slug), datastar.WithModeAppend())
}

// parseScheduleForm turns the editor's fields into events. Row i of every
// parallel field belongs to key[i]; its days are the checkboxes "days-<key>".
func parseScheduleForm(r *http.Request) ([]kumo.ScheduleEvent, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	f := r.Form
	keys := f["key"]
	for _, name := range []string{"at", "mode", "sph", "spc", "fan", "vane"} {
		if len(f[name]) != len(keys) {
			return nil, errors.New("malformed form")
		}
	}
	events := make([]kumo.ScheduleEvent, len(keys))
	for i, key := range keys {
		temp := func(s string) (*float64, error) {
			v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, fmt.Errorf("%w: row %d: temperature %q is not a number", control.ErrInvalid, i+1, s)
			}
			return &v, nil
		}
		e := kumo.ScheduleEvent{
			Days: f["days-"+key], StartTime: strings.ReplaceAll(f["at"][i], ":", ""),
			Mode: f["mode"][i], Fan: f["fan"][i], Vane: f["vane"][i],
		}
		if !strings.HasPrefix(key, "n") {
			e.ID = key
		}
		var err error
		if e.SpHeat, err = temp(f["sph"][i]); err != nil {
			return nil, err
		}
		if e.SpCool, err = temp(f["spc"][i]); err != nil {
			return nil, err
		}
		events[i] = e
	}
	return events, nil
}

func (d deps) handleScheduleSave(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.scheduleGuard(w, r)
	if !ok {
		return
	}
	slug := sn.Unit.Slug
	// Read the form before the stream starts: the server closes the request body once it writes.
	events, perr := parseScheduleForm(r)
	sse := datastar.NewSSE(w, r)
	fail := func(err error) {
		msg := strings.TrimPrefix(err.Error(), control.ErrInvalid.Error()+": ")
		sse.PatchElements(fmt.Sprintf(`<p class="sched-msg err" id="sched-msg-%s">%s</p>`, slug, html.EscapeString(msg)),
			datastar.WithSelectorf("#sched-msg-%s", slug), datastar.WithMode(datastar.ElementPatchModeReplace))
	}
	if perr != nil {
		fail(perr)
		return
	}
	_, err := d.Ctl.ApplySchedule(r.Context(), slug, events, "ui")
	switch {
	case errors.Is(err, control.ErrInvalid):
		fail(err)
	case err != nil:
		// Part of the change may have gone through: show what the cloud has now.
		fresh, _ := d.Store.Get(slug)
		d.patchEditor(sse, fresh, "Saving failed: "+err.Error()+". The schedule below is what the cloud has now.")
	default:
		fresh, _ := d.Store.Get(slug)
		d.patchBody(sse, fresh)
	}
}
