// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hughobrien/comfortd/internal/promwrite"
	"github.com/hughobrien/comfortd/internal/store"
)

// metricPusher sends a unit's state to Prometheus with remote write each time
// the store changes for it, stamped with that moment, so a change shows when
// it happened rather than at the next scrape, and changes closer together
// than a scrape interval are all kept.
//
// The store only changes on a poll or a push event, and with the socket up
// polls can be the backstop apart (15m by default), longer than Prometheus's
// 5m look-back. So a fresh unit whose state has not changed for a minute is
// sent again as it stands. A stale unit is not: it shows as a gap.
type metricPusher struct {
	m        *Metrics
	st       *store.Store
	q        *promwrite.Queue
	job      string
	now      func() time.Time
	interval func() time.Duration

	kick chan string
	last map[string]time.Time // newest sample time pushed, per unit
}

func newMetricPusher(m *Metrics, st *store.Store, w promwrite.Writer, job string, interval func() time.Duration) *metricPusher {
	p := &metricPusher{m: m, st: st, q: &promwrite.Queue{W: w}, job: job, now: time.Now, interval: interval,
		kick: make(chan string, 64), last: map[string]time.Time{}}
	m.RegisterPush(p.q.Len, p.q.Errors)
	st.Subscribe(func(u store.Update) {
		select {
		case p.kick <- u.Slug:
		default: // full: the minute's resend catches up
		}
	})
	return p
}

func (p *metricPusher) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case slug := <-p.kick:
			p.push(ctx, slug, false)
		case <-tick.C:
			for _, u := range p.st.Units() {
				if p.now().Sub(p.last[u.Slug]) >= 55*time.Second {
					p.push(ctx, u.Slug, true)
				}
			}
		}
	}
}

func (p *metricPusher) push(ctx context.Context, slug string, resend bool) {
	now := p.now()
	interval := p.interval()
	if resend {
		if s, ok := p.st.Get(slug); !ok || s.Stale(now, interval) {
			return
		}
	}
	// Strictly after the last sample: Prometheus refuses an older one.
	t := now
	if last := p.last[slug]; !t.After(last) {
		t = last.Add(time.Millisecond)
	}
	fams, err := p.m.Pushed(p.st, now, interval)
	if err != nil {
		slog.Warn("gather for push", "err", err)
		return
	}
	series := promwrite.FromFamilies(fams, t, func(l map[string]string) bool { return l["unit"] == slug },
		map[string]string{"job": p.job})
	if len(series) == 0 {
		return
	}
	p.q.Add(series...)
	p.last[slug] = t
	if err := p.q.Flush(ctx); err != nil {
		slog.Warn("push to prometheus", "err", err, "queued", p.q.Len())
	}
}
