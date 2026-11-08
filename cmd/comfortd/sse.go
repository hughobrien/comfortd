// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/starfederation/datastar-go/datastar"
)

// maxFeedRows is how many events an open tab keeps; the page starts with 50.
const maxFeedRows = 200

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

	// A tab opened before a deploy reloads when the stream reconnects to the new daemon.
	if err := sse.ExecuteScript(fmt.Sprintf(`var m=document.querySelector('meta[name=build]');if(m&&m.content!==%q)location.reload()`, templates.BuildID)); err != nil {
		return
	}

	// The schedule body ignores unit patches, so push it when the cloud's schedule changes.
	seen := map[string]string{}
	for _, v := range views {
		if sn, ok := d.Store.Get(v.Slug); ok {
			seen[v.Slug] = string(sn.Raw[store.EndpointSchedule])
		}
	}

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if err := d.emit(sse, ev, seen); err != nil {
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

func (d deps) emit(sse *datastar.ServerSentEventGenerator, ev pushEvent, seen map[string]string) error {
	if ev.Record != nil {
		if d.inactive(ev.Record.Unit) {
			return nil
		}
		if err := sse.PatchElementTempl(templates.FeedRow(*ev.Record),
			datastar.WithSelector("#feed"), datastar.WithModePrepend()); err != nil {
			return err
		}
		// An open tab keeps the newest rows only; the log itself is on disk.
		return sse.ExecuteScript(fmt.Sprintf("var f=document.getElementById('feed');while(f&&f.children.length>%d)f.lastElementChild.remove()", maxFeedRows))
	}
	sn, ok := d.Store.Get(ev.Slug)
	if !ok || d.inactive(ev.Slug) {
		return nil
	}
	now := d.Now()
	v := d.viewFor(sn, now)
	if err := sse.PatchElementTempl(templates.UnitSection(v),
		datastar.WithSelectorf("#unit-%s", v.Slug), datastar.WithModeOuter(),
		datastar.WithPatchElementsEventID("unit:"+v.Slug)); err != nil {
		return err
	}
	if cur := string(sn.Raw[store.EndpointSchedule]); cur != seen[v.Slug] {
		seen[v.Slug] = cur
		if v.Card.Schedule.Present {
			return sse.PatchElementTempl(templates.ScheduleBody(v.Card),
				datastar.WithSelectorf("#sched-body-%s", v.Slug), datastar.WithMode(datastar.ElementPatchModeReplace))
		}
	}
	return nil
}
