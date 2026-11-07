// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/store"
)

type deps struct {
	Store    *store.Store
	History  *history.Log
	Interval time.Duration
	Now      func() time.Time
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newMux(d deps) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		now := d.Now()
		var stale []string
		for _, sn := range d.Store.All() {
			if sn.Stale(now, d.Interval) {
				stale = append(stale, sn.Unit.Slug)
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if len(stale) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("stale: " + strings.Join(stale, ",") + "\n"))
			return
		}
		w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /v1/units", func(w http.ResponseWriter, r *http.Request) {
		type unitJSON struct {
			Slug         string            `json:"slug"`
			Name         string            `json:"name"`
			Serial       string            `json:"serial"`
			Stale        bool              `json:"stale"`
			LastDeviceOK *time.Time        `json:"lastDeviceOK"`
			Errors       map[string]string `json:"errors"`
		}
		now := d.Now()
		out := []unitJSON{}
		for _, sn := range d.Store.All() {
			u := unitJSON{Slug: sn.Unit.Slug, Name: sn.Unit.Name, Serial: sn.Unit.Serial,
				Stale: sn.Stale(now, d.Interval), Errors: sn.Errors}
			if !sn.LastDeviceOK.IsZero() {
				t := sn.LastDeviceOK
				u.LastDeviceOK = &t
			}
			out = append(out, u)
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /v1/units/{slug}/raw/{endpoint}", func(w http.ResponseWriter, r *http.Request) {
		ep := r.PathValue("endpoint")
		sn, ok := d.Store.Get(r.PathValue("slug"))
		if !ok || !store.ValidEndpoint(ep) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		raw := sn.Raw[ep]
		if raw == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not fetched"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	})

	mux.HandleFunc("GET /v1/history", func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, 1000)
		}
		out := []history.Record{}
		if d.History != nil {
			out = append(out, d.History.Recent(r.URL.Query().Get("unit"), limit)...)
		}
		writeJSON(w, http.StatusOK, out)
	})

	return mux
}
