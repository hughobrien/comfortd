// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log/slog"
	"sync"

	"github.com/hughobrien/comfortd/internal/history"
)

// pushEvent asks SSE clients to re-render a unit (Slug) or prepend a feed row (Record).
type pushEvent struct {
	Slug   string
	Record *history.Record
}

type pushHub struct {
	mu   sync.Mutex
	subs map[chan pushEvent]struct{}
}

func newPushHub() *pushHub { return &pushHub{subs: map[chan pushEvent]struct{}{}} }

// Subscribe returns a buffered event channel and an unsubscribe func.
func (h *pushHub) Subscribe() (<-chan pushEvent, func()) {
	ch := make(chan pushEvent, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Publish never blocks: a full subscriber drops the event.
func (h *pushHub) Publish(ev pushEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			slog.Debug("push: subscriber full, dropping event")
		}
	}
}
