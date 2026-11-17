// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"sync"
	"time"

	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/jsondiff"
	"github.com/hughobrien/comfortd/internal/store"
)

// sourceFunc names who made a changed field: "app" or "" (unknown). It may be nil.
type sourceFunc func(slug, endpoint, path, old, new string, at time.Time) string

// recordUpdate appends one history record per changed leaf. First sight of an
// endpoint (Old nil) records nothing.
func recordUpdate(h *history.Log, u store.Update, source sourceFunc) error {
	if u.Old == nil {
		return nil
	}
	oldF, err := jsondiff.Flatten(u.Old)
	if err != nil {
		return err
	}
	newF, err := jsondiff.Flatten(u.New)
	if err != nil {
		return err
	}
	var recs []history.Record
	for _, c := range jsondiff.Diff(oldF, newF) {
		r := history.Record{T: u.At.UTC(), Unit: u.Slug, Endpoint: u.Endpoint, Path: c.Path, Old: c.Old, New: c.New}
		if source != nil {
			r.Source = source(u.Slug, u.Endpoint, c.Path, c.Old, c.New, u.At)
		}
		recs = append(recs, r)
	}
	if len(recs) == 0 {
		return nil
	}
	return h.Append(recs...)
}

// holdWindow is how far apart an app's hold and the change it caused can be:
// the hold_update arrives first, the matching device update up to ~15 s later.
const holdWindow = 60 * time.Second

// intentFields are the device fields a person sets; schedule events, the wall
// thermostat and sensors also move them, which is why the app's hold is needed
// as evidence.
var intentFields = map[string]bool{"operationMode": true, "power": true, "spHeat": true, "spCool": true, "spAuto": true, "fanSpeed": true, "airDirection": true}

// sourceTracker attributes changes to the Comfort app. A change made in the
// app always opens a hold (tested on Office), so an intent-field change counts
// as the app's only when a hold_update with the hold enabled arrived for the
// unit within holdWindow and comfortd had not just sent anything itself.
type sourceTracker struct {
	mu       sync.Mutex
	holdSeen map[string]time.Time
	lastSent func(slug string) time.Time
}

// HoldPushed is the push worker's report of an applied hold_update.
func (t *sourceTracker) HoldPushed(slug string, enabled bool, at time.Time) {
	if !enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.holdSeen == nil {
		t.holdSeen = map[string]time.Time{}
	}
	t.holdSeen[slug] = at
}

func (t *sourceTracker) source(slug, endpoint, path, old, new string, at time.Time) string {
	switch {
	case endpoint == "device" && intentFields[path]:
	case endpoint == "zone" && path == "holdMode.enabled" && new == "true":
	default:
		return ""
	}
	t.mu.Lock()
	seen, ok := t.holdSeen[slug]
	t.mu.Unlock()
	if !ok {
		return ""
	}
	if d := at.Sub(seen); d > holdWindow || d < -holdWindow {
		return ""
	}
	if t.lastSent != nil {
		if sent := t.lastSent(slug); !sent.IsZero() && !sent.After(seen) && seen.Sub(sent) < holdWindow {
			return "" // our own request opened this hold
		}
	}
	return "app"
}
