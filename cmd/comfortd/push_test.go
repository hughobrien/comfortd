// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"
	"time"
)

func TestHubDropsWhenFull(t *testing.T) {
	h := newPushHub()
	_, unsub := h.Subscribe()
	defer unsub()
	start := time.Now()
	for range 100 {
		h.Publish(pushEvent{Slug: "office"})
	}
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("publish blocked: %v", d)
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := newPushHub()
	ch, unsub := h.Subscribe()
	h.Publish(pushEvent{Slug: "a"})
	if (<-ch).Slug != "a" {
		t.Fatal("no event")
	}
	unsub()
	h.Publish(pushEvent{Slug: "b"})
	select {
	case <-ch:
		t.Fatal("event after unsubscribe")
	default:
	}
}
