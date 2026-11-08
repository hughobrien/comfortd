// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/hughobrien/comfortd/internal/control"
)

// registerControl adds the command API and the UI control endpoints.
func (d deps) registerControl(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/units/{slug}/command", d.handleCommand)
	mux.HandleFunc("POST /ui/units/{slug}/mode", d.handleUIMode)
	mux.HandleFunc("POST /ui/units/{slug}/spheat", d.handleUISpHeat)
}

func (d deps) ctlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, control.ErrNotWritable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, control.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// known reports whether slug exists; it writes the 404 when not. A nil
// controller means control is off: everything is forbidden.
func (d deps) known(w http.ResponseWriter, slug string) bool {
	if _, ok := d.Store.Get(slug); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return false
	}
	if d.Ctl == nil {
		d.ctlError(w, control.ErrNotWritable)
		return false
	}
	return true
}

func (d deps) handleCommand(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return
	}
	slug := r.PathValue("slug")
	if !d.known(w, slug) {
		return
	}
	var body struct {
		Mode   *string  `json:"mode"`
		SpHeat *float64 `json:"spHeat"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body: " + err.Error()})
		return
	}
	if err := d.Ctl.Apply(slug, control.Intent{Mode: body.Mode, SpHeat: body.SpHeat}, "api"); err != nil {
		d.ctlError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// uiOK enforces the Datastar-Request header, which a plain cross-site form
// post cannot set.
func uiOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Datastar-Request") != "true" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (d deps) handleUIMode(w http.ResponseWriter, r *http.Request) {
	if !uiOK(w, r) {
		return
	}
	slug := r.PathValue("slug")
	if !d.known(w, slug) {
		return
	}
	m := r.URL.Query().Get("m")
	if err := d.Ctl.Apply(slug, control.Intent{Mode: &m}, "ui"); err != nil {
		d.ctlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d deps) handleUISpHeat(w http.ResponseWriter, r *http.Request) {
	if !uiOK(w, r) {
		return
	}
	slug := r.PathValue("slug")
	if !d.known(w, slug) {
		return
	}
	delta, err := strconv.ParseFloat(r.URL.Query().Get("delta"), 64)
	if err != nil || delta < -5 || delta > 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad delta"})
		return
	}
	snap, _ := d.Store.Get(slug)
	snap = d.Ctl.Overlay(snap)
	if snap.Device == nil || snap.Device.SpHeat == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "setpoint unknown"})
		return
	}
	v := *snap.Device.SpHeat + delta
	if err := d.Ctl.Apply(slug, control.Intent{SpHeat: &v}, "ui"); err != nil {
		d.ctlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
