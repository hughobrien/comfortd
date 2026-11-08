// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"embed"
	"fmt"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/store"
)

// datastarFile is the vendored datastar bundle; see ui/vendor/README.md.
const datastarFile = "ui/vendor/datastar-1.0.1.min.js"

//go:embed ui/style.css ui/vendor
var assetFS embed.FS

func serveAsset(file, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := assetFS.ReadFile(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	}
}

func (d deps) unitViews() []ui.UnitView {
	now := d.Now()
	var out []ui.UnitView
	for _, sn := range d.Store.All() {
		out = append(out, d.viewFor(sn, now))
	}
	return out
}

// viewFor builds a unit's view with pending control state overlaid.
func (d deps) viewFor(sn store.Snapshot, now time.Time) ui.UnitView {
	var cc ui.CardControl
	if d.Ctl != nil {
		slug := sn.Unit.Slug
		cc.Writable = d.Ctl.Writable(slug)
		cc.LastError = d.Ctl.LastError(slug)
		cc.Pending = map[string]string{}
		for k, v := range d.Ctl.Pending(slug) {
			cc.Pending[k] = fmt.Sprint(v)
		}
		sn = d.Ctl.Overlay(sn)
	}
	return ui.BuildUnitView(sn, d.changed(sn.Unit.Slug, now), now, d.Interval, d.Ann, cc)
}

func (d deps) changed(slug string, now time.Time) func(string) map[string]bool {
	return func(ep string) map[string]bool {
		if d.History == nil {
			return nil
		}
		return d.History.ChangedSince(slug, ep, now.Add(-recentWindow))
	}
}

func (d deps) feed() []history.Record {
	if d.History == nil {
		return nil
	}
	return d.History.Recent("", 50)
}

func (d deps) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	templ.Handler(templates.Page(d.unitViews(), d.feed())).ServeHTTP(w, r)
}
