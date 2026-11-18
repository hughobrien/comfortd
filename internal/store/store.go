// SPDX-License-Identifier: GPL-3.0-or-later

// Package store holds the latest raw API responses and parsed state per unit.
package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

const (
	EndpointZone     = "zone"
	EndpointDevice   = "device"
	EndpointProfile  = "profile"
	EndpointStatus   = "status"
	EndpointSensor   = "sensor"
	EndpointMhk2     = "mhk2"     // the wall thermostat, for zones with adapter.hasMhk2
	EndpointSchedule = "schedule" // this unit's events in the running season (built by the poller from the v4 API)
)

// ValidEndpoint reports whether e is a known endpoint name.
func ValidEndpoint(e string) bool {
	switch e {
	case EndpointZone, EndpointDevice, EndpointProfile, EndpointStatus, EndpointSensor, EndpointMhk2, EndpointSchedule:
		return true
	}
	return false
}

type Unit struct{ Slug, Name, Serial, ZoneID, SiteID string }

// Slug lowercases name and joins alphanumeric runs with hyphens.
func Slug(name string) string {
	var parts []string
	var cur strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return strings.Join(parts, "-")
}

type Snapshot struct {
	Unit         Unit
	Raw          map[string]json.RawMessage
	At           map[string]time.Time
	Errors       map[string]string
	Device       *kumo.Device
	Profile      *kumo.Profile
	Zone         *kumo.Zone
	LastDeviceOK time.Time
}

// AdapterLimits is the adapter's Min/Max setpoint limits from the status
// endpoint (minSetPoint, maxSetPoint), if it has a usable pair.
func (s Snapshot) AdapterLimits() (lo, hi float64, ok bool) {
	var st struct {
		Min *float64 `json:"minSetPoint"`
		Max *float64 `json:"maxSetPoint"`
	}
	if json.Unmarshal(s.Raw[EndpointStatus], &st) != nil || st.Min == nil || st.Max == nil || *st.Min >= *st.Max {
		return 0, 0, false
	}
	return *st.Min, *st.Max, true
}

// SetpointRange is the setpoint range the unit allows under a profile limit
// key ("heat", "cool" or "auto"). It is the capability profile's range for the
// key, with the adapter's limits from the status endpoint (minSetPoint,
// maxSetPoint: House's "setpoint limit" setting in the Comfort app) applied the
// way the app applies them (read off House on 2026-11-17, with the limit on
// and off): the adapter minimum floors cool, the adapter maximum caps heat, and
// auto is narrowed at both ends. The other ends are the profile's alone. With
// no profile, the status range alone.
func (s Snapshot) SetpointRange(key string) (lo, hi float64, ok bool) {
	sMin, sMax, hasStatus := s.AdapterLimits()
	if s.Profile == nil {
		if hasStatus {
			return sMin, sMax, true
		}
		return 0, 0, false
	}
	lo, hasLo := s.Profile.Min[key]
	hi, hasHi := s.Profile.Max[key]
	if !hasLo || !hasHi {
		return 0, 0, false
	}
	if hasStatus {
		switch key {
		case "cool":
			lo = max(lo, sMin)
		case "heat":
			hi = min(hi, sMax)
		default:
			lo, hi = max(lo, sMin), min(hi, sMax)
		}
	}
	return lo, hi, lo < hi
}

// Stale reports whether the device data is too old or the unit is offline.
func (s Snapshot) Stale(now time.Time, interval time.Duration) bool {
	if s.Device != nil && !s.Device.Connected {
		return true
	}
	return now.Sub(s.LastDeviceOK) > 3*interval
}

type Update struct {
	Slug, Endpoint string
	Old, New       json.RawMessage
	At             time.Time
	Patched        bool // from Patch (a push), not a fetched body
}

type Store struct {
	mu   sync.Mutex
	snap map[string]*Snapshot
	subs []subscriber
}

func New(units []Unit) *Store {
	s := &Store{snap: map[string]*Snapshot{}}
	for _, u := range units {
		s.snap[u.Slug] = &Snapshot{
			Unit:   u,
			Raw:    map[string]json.RawMessage{},
			At:     map[string]time.Time{},
			Errors: map[string]string{},
		}
	}
	return s
}

func (s *Store) Units() []Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Unit, 0, len(s.snap))
	for _, sn := range s.snap {
		out = append(out, sn.Unit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// parsed holds what a stored body is parsed into, by endpoint.
type parsed struct {
	dev  *kumo.Device
	prof *kumo.Profile
	zone *kumo.Zone
}

func parse(endpoint string, raw json.RawMessage) (parsed, error) {
	var p parsed
	switch endpoint {
	case EndpointDevice:
		d, err := kumo.ParseDevice(raw)
		if err != nil {
			return p, err
		}
		p.dev = &d
	case EndpointProfile:
		pr, err := kumo.ParseProfile(raw)
		if err != nil {
			return p, err
		}
		p.prof = &pr
	case EndpointZone:
		zs, err := kumo.ParseZones(append(append(json.RawMessage("["), raw...), ']'))
		if err != nil {
			return p, err
		}
		if len(zs) == 1 {
			p.zone = &zs[0]
		}
	}
	return p, nil
}

// commit stores raw for endpoint; the caller holds s.mu.
func (sn *Snapshot) commit(endpoint string, raw json.RawMessage, p parsed, at time.Time) {
	sn.Raw[endpoint] = append(json.RawMessage(nil), raw...)
	sn.At[endpoint] = at
	delete(sn.Errors, endpoint)
	if p.dev != nil {
		sn.Device = p.dev
		sn.LastDeviceOK = at
	}
	if p.prof != nil {
		sn.Profile = p.prof
	}
	if p.zone != nil {
		sn.Zone = p.zone
	}
}

// notify calls the subscribers; onlyAlways limits it to those that asked to
// hear about every write.
func (s *Store) notify(u Update, onlyAlways bool) {
	s.mu.Lock()
	subs := slices.Clone(s.subs)
	s.mu.Unlock()
	for _, sub := range subs {
		if onlyAlways && !sub.always {
			continue
		}
		sub.fn(u)
	}
}

// Put stores a fetched body and tells every subscriber.
func (s *Store) Put(slug, endpoint string, raw json.RawMessage, at time.Time) error {
	p, err := parse(endpoint, raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	sn, ok := s.snap[slug]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("store: unknown unit %q", slug)
	}
	old := sn.Raw[endpoint]
	sn.commit(endpoint, raw, p, at)
	s.mu.Unlock()
	s.notify(Update{Slug: slug, Endpoint: endpoint, Old: old, New: raw, At: at}, false)
	return nil
}

// Patch rewrites the stored body of endpoint atomically: fn gets the current
// body (nil if none) and returns the new one; it runs under the store lock and
// must not call the store. If fn returns an error nothing changes. If the new
// body means the same JSON as the old, only the timestamps advance and only
// SubscribeAlways subscribers are told (changed is false); otherwise it is
// stored like a Put and everyone is told.
func (s *Store) Patch(slug, endpoint string, fn func(old json.RawMessage) (json.RawMessage, error), at time.Time) (changed bool, err error) {
	s.mu.Lock()
	sn, ok := s.snap[slug]
	if !ok {
		s.mu.Unlock()
		return false, fmt.Errorf("store: unknown unit %q", slug)
	}
	old := sn.Raw[endpoint]
	raw, err := fn(old)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	p, err := parse(endpoint, raw)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	changed = !sameJSON(old, raw)
	if changed {
		sn.commit(endpoint, raw, p, at)
	} else {
		sn.At[endpoint] = at
		delete(sn.Errors, endpoint)
		if p.dev != nil {
			sn.LastDeviceOK = at
		}
	}
	s.mu.Unlock()
	s.notify(Update{Slug: slug, Endpoint: endpoint, Old: old, New: raw, At: at, Patched: true}, !changed)
	return changed, nil
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (s *Store) PutError(slug, endpoint string, err error, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn, ok := s.snap[slug]; ok {
		sn.Errors[endpoint] = err.Error()
	}
}

func (s *Store) Get(slug string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, ok := s.snap[slug]
	if !ok {
		return Snapshot{}, false
	}
	return clone(sn), true
}

func (s *Store) All() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Snapshot, 0, len(s.snap))
	for _, sn := range s.snap {
		out = append(out, clone(sn))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit.Slug < out[j].Unit.Slug })
	return out
}

type subscriber struct {
	fn     func(Update)
	always bool
}

// Subscribe registers fn for every Put and for each Patch that changes the data.
func (s *Store) Subscribe(fn func(Update)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, subscriber{fn: fn})
}

// SubscribeAlways is Subscribe, but fn also hears Patches that changed nothing
// (the controller needs them to confirm a sent value that equals the stored one).
func (s *Store) SubscribeAlways(fn func(Update)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, subscriber{fn: fn, always: true})
}

func clone(sn *Snapshot) Snapshot {
	c := *sn
	c.Raw = maps.Clone(sn.Raw)
	c.At = maps.Clone(sn.At)
	c.Errors = maps.Clone(sn.Errors)
	if sn.Device != nil {
		d := *sn.Device
		d.RealValues = maps.Clone(sn.Device.RealValues)
		c.Device = &d
	}
	if sn.Profile != nil {
		p := *sn.Profile
		p.Min, p.Max = maps.Clone(p.Min), maps.Clone(p.Max)
		c.Profile = &p
	}
	if sn.Zone != nil {
		z := *sn.Zone
		c.Zone = &z
	}
	return c
}
