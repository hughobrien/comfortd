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

	// KickNeeded, when set and false, makes Kick do nothing (the socket's echo
	// confirms commands instead).
	KickNeeded func() bool

	mu        sync.Mutex
	ctx       context.Context
	connected func() bool   // socket state, nil = no socket
	backstop  time.Duration // full-poll interval while the socket is up
	lastFull  time.Time
	wake      chan struct{}
}

func New(api API, st *store.Store, interval time.Duration) *Poller {
	return &Poller{
		api: api, st: st, interval: interval,
		slowEvery:  6 * time.Hour,
		kickDelays: []time.Duration{3 * time.Second, 8 * time.Second, 15 * time.Second},
		wake:       make(chan struct{}, 1),
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

// UseSocket tells the poller a push socket is feeding the store: while
// connected() is true it polls every backstop (and also refreshes the MHK2,
// sensor and schedule each time); otherwise at its normal interval.
func (p *Poller) UseSocket(connected func() bool, backstop time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected, p.backstop = connected, backstop
}

// Wake makes Run re-evaluate when the next poll is due (the socket changed state).
func (p *Poller) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// PollOnce fetches zones (once per site) and each unit's device.
func (p *Poller) PollOnce(ctx context.Context) {
	p.mu.Lock()
	p.lastFull = time.Now()
	p.mu.Unlock()
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

// zoneFlag reports whether the unit's zone has adapter.<flag> true. It needs
// the zone to have been polled.
func (p *Poller) zoneFlag(slug, flag string) bool {
	sn, ok := p.st.Get(slug)
	if !ok {
		return false
	}
	var z struct {
		Adapter map[string]any `json:"adapter"`
	}
	if json.Unmarshal(sn.Raw[store.EndpointZone], &z) != nil {
		return false
	}
	v, _ := z.Adapter[flag].(bool)
	return v
}

// hasSensor reports whether the zone says there is a wireless sensor.
func (p *Poller) hasSensor(slug string) bool { return p.zoneFlag(slug, "hasSensor") }

// PollMhk2 fetches the wall thermostat's endpoint for zones that report one
// (adapter.hasMhk2); the others would answer 404.
func (p *Poller) PollMhk2(ctx context.Context) {
	for _, u := range p.st.Units() {
		if !p.zoneFlag(u.Slug, "hasMhk2") {
			continue
		}
		raw, err := p.api.Get(ctx, "/devices/"+u.Serial+"/mhk2")
		p.record(u.Slug, store.EndpointMhk2, raw, err)
	}
}

// PollSensor fetches the sensor endpoint for units whose zone reports a
// wireless sensor; the others would answer 404.
func (p *Poller) PollSensor(ctx context.Context) {
	for _, u := range p.st.Units() {
		if !p.hasSensor(u.Slug) {
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
	ID     string              `json:"id"`
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
			out, _ := json.Marshal(map[string]any{"season": season.Name, "seasonId": season.ID, "scheduleId": scheds[j].ID, "events": scheds[j].Events})
			p.record(u.Slug, store.EndpointSchedule, out, nil)
		}
	}
}

// Run polls until ctx is done.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()

	p.PollOnce(ctx) // first, for the zones: they say which units have a sensor or a thermostat
	p.PollSlow(ctx)
	p.PollSensor(ctx)
	p.PollMhk2(ctx)
	p.PollSchedule(ctx)
	p.PollOnce(ctx) // again, so the first view has the profile
	slow := time.NewTicker(p.slowEvery)
	defer slow.Stop()
	for {
		p.mu.Lock()
		up := p.connected != nil && p.connected()
		wait := p.interval
		if up {
			wait = p.backstop
		}
		due := time.Until(p.lastFull.Add(wait))
		p.mu.Unlock()
		timer := time.NewTimer(max(due, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-p.wake:
			timer.Stop()
		case <-timer.C:
			p.PollOnce(ctx)
			if up { // the push feed does not cover these, so the backstop does
				p.PollMhk2(ctx)
				p.PollSensor(ctx)
				p.PollSchedule(ctx)
			}
		case <-slow.C:
			timer.Stop()
			p.PollSlow(ctx)
			p.PollSensor(ctx)
			p.PollMhk2(ctx)
			p.PollSchedule(ctx)
		}
	}
}

// Kick schedules extra device polls for slug shortly after a change. It never blocks.
func (p *Poller) Kick(slug string) {
	if p.KickNeeded != nil && !p.KickNeeded() {
		return
	}
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
