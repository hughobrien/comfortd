// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"github.com/starfederation/datastar-go/datastar"
)

const recentWindow = 10 * time.Minute

var keepaliveInterval = 30 * time.Second

func (d deps) handleSSE(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Debug("sse: clear write deadline", "err", err)
	}
	w.Header().Set("X-Accel-Buffering", "no")
	sse := datastar.NewSSE(w, r)

	// Subscribe before the initial pass so nothing is lost in the gap.
	var events <-chan pushEvent
	if d.Hub != nil {
		ch, unsub := d.Hub.Subscribe()
		defer unsub()
		events = ch
	}

	reconnect := r.Header.Get("Last-Event-ID") != ""
	views := d.unitViews()
	if reconnect {
		for _, v := range views {
			if err := sse.PatchElementTempl(templates.UnitSection(v),
				datastar.WithSelectorf("#unit-%s", v.Slug), datastar.WithModeOuter(),
				datastar.WithPatchElementsEventID("unit:"+v.Slug)); err != nil {
				return
			}
		}
	} else {
		for i, v := range views {
			opts := []datastar.PatchElementOption{datastar.WithSelector("#units"), datastar.WithPatchElementsEventID("unit:" + v.Slug)}
			if i == 0 {
				opts = append(opts, datastar.WithModeInner())
			} else {
				opts = append(opts, datastar.WithModeAppend())
			}
			if err := sse.PatchElementTempl(templates.UnitSection(v), opts...); err != nil {
				return
			}
		}
	}

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if err := d.emit(sse, ev); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

func (d deps) emit(sse *datastar.ServerSentEventGenerator, ev pushEvent) error {
	if ev.Record != nil {
		return sse.PatchElementTempl(templates.FeedRow(*ev.Record),
			datastar.WithSelector("#feed"), datastar.WithModePrepend())
	}
	sn, ok := d.Store.Get(ev.Slug)
	if !ok {
		return nil
	}
	now := d.Now()
	v := ui.BuildUnitView(sn, d.changed(sn.Unit.Slug, now), now, d.Interval, d.Ann, ui.CardControl{})
	return sse.PatchElementTempl(templates.UnitSection(v),
		datastar.WithSelectorf("#unit-%s", v.Slug), datastar.WithModeOuter(),
		datastar.WithPatchElementsEventID("unit:"+v.Slug))
}
