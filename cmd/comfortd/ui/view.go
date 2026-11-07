// SPDX-License-Identifier: GPL-3.0-or-later

// Package ui builds the view models rendered by the explorer templates.
package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/hughobrien/comfortd/internal/annotate"
	"github.com/hughobrien/comfortd/internal/jsondiff"
	"github.com/hughobrien/comfortd/internal/store"
)

type Row struct {
	Path, Value                      string
	Changed, Noisy                   bool
	Label, Decoded, Confidence, Note string
}

type EndpointView struct {
	Name, At, Error string
	Rows            []Row
}

type UnitView struct {
	Slug, Name, Serial string
	Stale              bool
	LastOK             string
	Endpoints          []EndpointView
	Card               CardView
	Unknown            []Row // device and zone rows with no annotation, excluding noisy ones
}

var endpointOrder = []string{store.EndpointZone, store.EndpointDevice, store.EndpointProfile, store.EndpointStatus, store.EndpointSensor}

var noisy = map[string]bool{"updatedAt": true, "lastStatusChangeAt": true, "rssi": true, "routerRssi": true, "lastUpdated": true}

func isNoisy(path string) bool {
	last := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		last = path[i+1:]
	}
	if i := strings.IndexByte(last, '['); i >= 0 {
		last = last[:i]
	}
	return noisy[last]
}

// BuildUnitView flattens every fetched endpoint into sorted rows. changed
// returns the recently changed paths for an endpoint.
func BuildUnitView(s store.Snapshot, changed func(endpoint string) map[string]bool, now time.Time, interval time.Duration, ann *annotate.Set, ctl CardControl) UnitView {
	v := UnitView{Slug: s.Unit.Slug, Name: s.Unit.Name, Serial: s.Unit.Serial, Stale: s.Stale(now, interval), Card: BuildCard(s, ann, ctl, now, interval)}
	if !s.LastDeviceOK.IsZero() {
		v.LastOK = s.LastDeviceOK.Local().Format("15:04:05")
	}
	for _, name := range endpointOrder {
		raw, hasRaw := s.Raw[name]
		errMsg := s.Errors[name]
		if !hasRaw && errMsg == "" {
			continue
		}
		ev := EndpointView{Name: name, Error: errMsg}
		if at, ok := s.At[name]; ok {
			ev.At = at.Local().Format("15:04:05")
		}
		if hasRaw {
			flat, err := jsondiff.Flatten(raw)
			if err != nil {
				ev.Error = err.Error()
			}
			var ch map[string]bool
			if changed != nil {
				ch = changed(name)
			}
			for p, val := range flat {
				r := Row{Path: p, Value: val, Changed: ch[p], Noisy: isNoisy(p)}
				f, annotated := ann.Lookup(name, p)
				if annotated {
					r.Label, r.Confidence, r.Note = f.Label, f.Confidence, f.Note
					if len(f.Values) > 0 {
						if m, ok := f.Decode(val); ok {
							r.Decoded = m
						} else {
							r.Decoded = "(undecoded)"
						}
					}
				} else if !r.Noisy && (name == store.EndpointDevice || name == store.EndpointZone) {
					v.Unknown = append(v.Unknown, r)
				}
				ev.Rows = append(ev.Rows, r)
			}
			sort.Slice(ev.Rows, func(i, j int) bool { return ev.Rows[i].Path < ev.Rows[j].Path })
		}
		v.Endpoints = append(v.Endpoints, ev)
	}
	sort.SliceStable(v.Unknown, func(i, j int) bool { return v.Unknown[i].Path < v.Unknown[j].Path })
	return v
}
