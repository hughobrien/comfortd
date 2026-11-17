// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

// pushBeat is the staleness base while the push socket is up (stale after
// three of it): a unit that has sent nothing for six minutes on a live socket
// is not fine.
const pushBeat = 2 * time.Minute

type pushOptions struct {
	Client       *kumo.Client
	Store        *store.Store
	Poller       *poller.Poller
	SocketURL    string
	Backstop     time.Duration
	PollInterval time.Duration
	Metrics      *Metrics
	LastSent     func(slug string) time.Time
	Go           func(func()) // starts a worker goroutine
}

type pushWiring struct {
	Push    *poller.Push
	Socket  *kumo.Socket
	Source  *sourceTracker
	Beat    func() time.Duration
	missing func(slug string, fields []string)
}

// wirePush connects the Socket.IO feed to the store and starts its workers.
func wirePush(o pushOptions, ctx context.Context) *pushWiring {
	push := poller.NewPush(o.Store, o.Poller)
	src := &sourceTracker{lastSent: o.LastSent}
	push.OnHold = src.HoldPushed
	push.OnEventCount = o.Metrics.SocketEvent
	o.Poller.KickNeeded = push.KickNeeded
	o.Poller.UseSocket(push.Connected, o.Backstop)

	var serials []string
	for _, u := range o.Store.Units() {
		serials = append(serials, u.Serial)
	}
	sock := o.Client.NewSocket(kumo.SocketOptions{
		URL: o.SocketURL, Serials: serials, OnEvent: push.OnEvent, OnState: push.OnState,
	})
	o.Metrics.RegisterSocket(push.Connected, sock.Reconnects, push.Dropped)

	// A poll that finds an intent field changed while the socket was up means
	// the socket missed it (or our own command's echo is not pushed).
	o.Store.Subscribe(func(u store.Update) {
		if u.Patched || u.Endpoint != store.EndpointDevice || !push.Connected() {
			return
		}
		if f := missedChanges(u.Old, u.New); len(f) > 0 {
			for _, name := range f {
				o.Metrics.SocketMissed(name)
			}
			slog.Info("poll found changes the socket did not push", "unit", u.Slug, "fields", f)
		}
	})

	o.Go(func() { push.Run(ctx) })
	o.Go(func() { sock.Run(ctx) })
	return &pushWiring{
		Push: push, Socket: sock, Source: src,
		Beat: func() time.Duration {
			if push.Connected() {
				return pushBeat
			}
			return o.PollInterval
		},
	}
}

// missedChanges lists the intent fields that differ between two device bodies
// (sorted); nothing on first sight.
func missedChanges(old, new json.RawMessage) []string {
	if len(old) == 0 {
		return nil
	}
	var a, b map[string]any
	if json.Unmarshal(old, &a) != nil || json.Unmarshal(new, &b) != nil {
		return nil
	}
	var out []string
	for k := range intentFields {
		if !reflect.DeepEqual(a[k], b[k]) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
