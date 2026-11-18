// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/hughobrien/comfortd/internal/control"
	"github.com/starfederation/datastar-go/datastar"
)

// registerLimits adds the Min/Max setpoints endpoint. It answers with a
// datastar patch of the message line.
func (d deps) registerLimits(mux *http.ServeMux) {
	mux.HandleFunc("POST /ui/units/{slug}/limits", d.handleLimits)
}

func (d deps) handleLimits(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !uiOK(w, r) || !d.known(w, slug) {
		return
	}
	if !d.Ctl.LimitsEnabled(slug) {
		d.ctlError(w, control.ErrNotWritable)
		return
	}
	// Read the form before the stream starts: the server closes the request body once it writes.
	perr := r.ParseForm()
	enabled := r.PostForm.Get("enabled") != ""
	minS, maxS := strings.TrimSpace(r.PostForm.Get("min")), strings.TrimSpace(r.PostForm.Get("max"))
	sse := datastar.NewSSE(w, r)
	say := func(class, msg string) {
		sse.PatchElements(fmt.Sprintf(`<p class="%s" id="limits-msg-%s">%s</p>`, class, slug, html.EscapeString(msg)),
			datastar.WithSelectorf("#limits-msg-%s", slug), datastar.WithMode(datastar.ElementPatchModeReplace))
	}
	lo, e1 := strconv.ParseFloat(minS, 64)
	hi, e2 := strconv.ParseFloat(maxS, 64)
	var err error
	if perr != nil || e1 != nil || e2 != nil {
		err = fmt.Errorf("%w: enter both limits as numbers", control.ErrInvalid)
	} else {
		// The account preference first (as the app does), then the limits, each only if it changed.
		if on, known := d.Ctl.LimitsOn(); known && on != enabled {
			err = d.Ctl.SetLimitsOn(r.Context(), slug, enabled)
		}
		if sn, ok := d.Store.Get(slug); err == nil && ok {
			if cur, curHi, has := sn.AdapterLimits(); !has || cur != lo || curHi != hi {
				err = d.Ctl.SetLimits(r.Context(), slug, lo, hi, "ui")
			}
		}
	}
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), control.ErrInvalid.Error()+": ")
		if !errors.Is(err, control.ErrInvalid) {
			msg = "the cloud did not take it: " + msg
		}
		say("hold-msg err", msg)
		return
	}
	say("hold-msg", "Saved.")
}
