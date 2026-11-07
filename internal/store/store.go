// SPDX-License-Identifier: GPL-3.0-or-later

// Package store holds the latest raw API responses and parsed state per unit.
package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo"
)

const (
	EndpointZone    = "zone"
	EndpointDevice  = "device"
	EndpointProfile = "profile"
	EndpointStatus  = "status"
	EndpointSensor  = "sensor"
)

// ValidEndpoint reports whether e is a known endpoint name.
func ValidEndpoint(e string) bool {
	switch e {
	case EndpointZone, EndpointDevice, EndpointProfile, EndpointStatus, EndpointSensor:
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
}

type Store struct {
	mu   sync.Mutex
	snap map[string]*Snapshot
	subs []func(Update)
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

func (s *Store) Put(slug, endpoint string, raw json.RawMessage, at time.Time) error {
	var dev *kumo.Device
	var prof *kumo.Profile
	switch endpoint {
	case EndpointDevice:
		d, err := kumo.ParseDevice(raw)
		if err != nil {
			return err
		}
		dev = &d
	case EndpointProfile:
		p, err := kumo.ParseProfile(raw)
		if err != nil {
			return err
		}
		prof = &p
	}
	var zone *kumo.Zone
	if endpoint == EndpointZone {
		zs, err := kumo.ParseZones(append(append(json.RawMessage("["), raw...), ']'))
		if err != nil {
			return err
		}
		if len(zs) == 1 {
			zone = &zs[0]
		}
	}

	s.mu.Lock()
	sn, ok := s.snap[slug]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("store: unknown unit %q", slug)
	}
	old := sn.Raw[endpoint]
	sn.Raw[endpoint] = append(json.RawMessage(nil), raw...)
	sn.At[endpoint] = at
	delete(sn.Errors, endpoint)
	if dev != nil {
		sn.Device = dev
		sn.LastDeviceOK = at
	}
	if prof != nil {
		sn.Profile = prof
	}
	if zone != nil {
		sn.Zone = zone
	}
	subs := slices.Clone(s.subs)
	s.mu.Unlock()

	u := Update{Slug: slug, Endpoint: endpoint, Old: old, New: raw, At: at}
	for _, fn := range subs {
		fn(u)
	}
	return nil
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

func (s *Store) Subscribe(fn func(Update)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
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
