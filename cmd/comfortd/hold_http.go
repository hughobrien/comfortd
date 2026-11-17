// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/internal/control"
	"github.com/starfederation/datastar-go/datastar"
)

// registerHold adds the hold control's endpoint. It answers with a datastar
// patch of the message line; the control itself refreshes from the poll the
// write kicks.
func (d deps) registerHold(mux *http.ServeMux) {
	mux.HandleFunc("POST /ui/units/{slug}/hold", d.handleHold)
}

func (d deps) handleHold(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.scheduleGuard(w, r) // same checks: datastar header, known, writable
	if !ok {
		return
	}
	slug := sn.Unit.Slug
	// Read the form before the stream starts: the server closes the request body once it writes.
	perr := r.ParseForm()
	mode, at := r.PostForm.Get("mode"), strings.TrimSpace(r.PostForm.Get("at"))
	sse := datastar.NewSSE(w, r)
	say := func(class, msg string) {
		sse.PatchElements(fmt.Sprintf(`<p class="%s" id="hold-msg-%s">%s</p>`, class, slug, html.EscapeString(msg)),
			datastar.WithSelectorf("#hold-msg-%s", slug), datastar.WithMode(datastar.ElementPatchModeReplace))
	}
	var err error
	switch {
	case perr != nil:
		err = fmt.Errorf("%w: malformed form", control.ErrInvalid)
	case mode == "none":
		err = d.Ctl.EndHold(r.Context(), slug, "ui")
	case mode == "permanent":
		err = d.Ctl.SetHold(r.Context(), slug, nil, "ui")
	case mode == "next":
		h := ui.BuildHold(sn, true, d.Now())
		if !h.CanNext {
			err = fmt.Errorf("%w: the schedule has no next event", control.ErrInvalid)
			break
		}
		err = d.Ctl.SetHold(r.Context(), slug, &h.NextAt, "ui")
	case mode == "until":
		var t time.Time
		if t, err = ui.HoldTimeFromClock(sn, d.Now(), at); err == nil {
			err = d.Ctl.SetHold(r.Context(), slug, &t, "ui")
		}
	default:
		err = fmt.Errorf("%w: unknown hold choice %q", control.ErrInvalid, mode)
	}
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), control.ErrInvalid.Error()+": ")
		if !errors.Is(err, control.ErrInvalid) && !errors.Is(err, ui.ErrBadTime) {
			msg = "the cloud did not take it: " + msg
		}
		say("hold-msg err", msg)
		return
	}
	say("hold-msg", "")
}
