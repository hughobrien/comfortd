// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

type Poller struct {
	api        API
	st         *store.Store
	interval   time.Duration
	slowEvery  time.Duration
	kickDelays []time.Duration
	sensors    map[string]bool // slugs whose sensor endpoint is polled

	mu  sync.Mutex
	ctx context.Context
}

func New(api API, st *store.Store, interval time.Duration) *Poller {
	return &Poller{
		api: api, st: st, interval: interval,
		slowEvery:  6 * time.Hour,
		kickDelays: []time.Duration{3 * time.Second, 8 * time.Second, 15 * time.Second},
	}
}

func (p *Poller) record(slug, endpoint string, raw json.RawMessage, err error) {
	now := time.Now()
	if err == nil {
		err = p.st.Put(slug, endpoint, raw, now)
	}
	if err != nil {
		if errors.Is(err, kumo.ErrNotFound) {
			err = errors.New("not found")
		}
		slog.Warn("poll failed", "unit", slug, "endpoint", endpoint, "err", err)
		p.st.PutError(slug, endpoint, err, now)
	}
}

func (p *Poller) pollDevice(ctx context.Context, u store.Unit) {
	raw, err := p.api.Get(ctx, "/devices/"+u.Serial)
	p.record(u.Slug, store.EndpointDevice, raw, err)
}

// PollOnce fetches zones (once per site) and each unit's device.
func (p *Poller) PollOnce(ctx context.Context) {
	units := p.st.Units()
	sites := map[string][]store.Unit{}
	for _, u := range units {
		sites[u.SiteID] = append(sites[u.SiteID], u)
	}
	for site, us := range sites {
		raw, err := p.api.Get(ctx, "/sites/"+site+"/zones")
		var zones []json.RawMessage
		if err == nil {
			err = json.Unmarshal(raw, &zones)
		}
		if err != nil {
			for _, u := range us {
				p.record(u.Slug, store.EndpointZone, nil, err)
			}
			continue
		}
		for _, u := range us {
			found := false
			for _, z := range zones {
				var id struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(z, &id) == nil && id.ID == u.ZoneID {
					p.record(u.Slug, store.EndpointZone, z, nil)
					found = true
					break
				}
			}
			if !found {
				p.record(u.Slug, store.EndpointZone, nil, errors.New("zone missing from response"))
			}
		}
	}
	for _, u := range units {
		p.pollDevice(ctx, u)
	}
}

// PollSlow fetches profile and status per unit.
func (p *Poller) PollSlow(ctx context.Context) {
	for _, u := range p.st.Units() {
		for _, ep := range []string{store.EndpointProfile, store.EndpointStatus} {
			raw, err := p.api.Get(ctx, "/devices/"+u.Serial+"/"+ep)
			p.record(u.Slug, ep, raw, err)
		}
	}
}

// SensorUnits sets which unit slugs have the sensor endpoint polled; the
// default is none, since most units have no wireless sensor and would 404.
// Call it before Run.
func (p *Poller) SensorUnits(slugs ...string) {
	p.sensors = map[string]bool{}
	for _, s := range slugs {
		p.sensors[s] = true
	}
}

// PollSensor fetches the sensor endpoint for the units named in SensorUnits.
func (p *Poller) PollSensor(ctx context.Context) {
	for _, u := range p.st.Units() {
		if !p.sensors[u.Slug] {
			continue
		}
		raw, err := p.api.Get(ctx, "/devices/"+u.Serial+"/sensor")
		p.record(u.Slug, store.EndpointSensor, raw, err)
	}
}

type scheduleSeason struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsRunning bool   `json:"isRunning"`
}

type zoneSchedule struct {
	Zone   struct{ ID string } `json:"zone"`
	Events json.RawMessage     `json:"events"`
}

// PollSchedule fetches each site's running schedule season (v4 API) and
// stores every unit's events under the schedule endpoint as
// {"season": name, "events": [...]}.
func (p *Poller) PollSchedule(ctx context.Context) {
	sites := map[string][]store.Unit{}
	for _, u := range p.st.Units() {
		sites[u.SiteID] = append(sites[u.SiteID], u)
	}
	for site, us := range sites {
		fail := func(err error) {
			for _, u := range us {
				p.record(u.Slug, store.EndpointSchedule, nil, err)
			}
		}
		raw, err := p.api.Get(ctx, "/v4/sites/"+site+"/schedule-seasons")
		if err != nil {
			fail(err)
			continue
		}
		var seasons []scheduleSeason
		if err := json.Unmarshal(raw, &seasons); err != nil {
			fail(fmt.Errorf("schedule seasons: %w", err))
			continue
		}
		i := slices.IndexFunc(seasons, func(s scheduleSeason) bool { return s.IsRunning })
		if i < 0 {
			fail(errors.New("no schedule season is running"))
			continue
		}
		season := seasons[i]
		raw, err = p.api.Get(ctx, "/v4/schedule-seasons/"+season.ID+"/schedules")
		if err != nil {
			fail(err)
			continue
		}
		var scheds []zoneSchedule
		if err := json.Unmarshal(raw, &scheds); err != nil {
			fail(fmt.Errorf("schedules: %w", err))
			continue
		}
		for _, u := range us {
			j := slices.IndexFunc(scheds, func(s zoneSchedule) bool { return s.Zone.ID == u.ZoneID })
			if j < 0 {
				p.record(u.Slug, store.EndpointSchedule, nil, errors.New("no schedule for this zone in the running season"))
				continue
			}
			out, _ := json.Marshal(map[string]any{"season": season.Name, "events": scheds[j].Events})
			p.record(u.Slug, store.EndpointSchedule, out, nil)
		}
	}
}

// Run polls until ctx is done.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()

	p.PollSensor(ctx)
	p.PollSlow(ctx)
	p.PollSchedule(ctx)
	p.PollOnce(ctx)
	fast := time.NewTicker(p.interval)
	slow := time.NewTicker(p.slowEvery)
	defer fast.Stop()
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-fast.C:
			p.PollOnce(ctx)
		case <-slow.C:
			p.PollSlow(ctx)
			p.PollSchedule(ctx)
		}
	}
}

// Kick schedules extra device polls for slug shortly after a change. It never blocks.
func (p *Poller) Kick(slug string) {
	var unit *store.Unit
	for _, u := range p.st.Units() {
		if u.Slug == slug {
			u := u
			unit = &u
		}
	}
	if unit == nil {
		return
	}
	p.mu.Lock()
	ctx := p.ctx
	p.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		var elapsed time.Duration
		for _, d := range p.kickDelays {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d - elapsed):
			}
			elapsed = d
			p.pollDevice(ctx, *unit)
		}
	}()
}
