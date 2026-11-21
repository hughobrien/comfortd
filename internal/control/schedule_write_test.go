// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

// hookedSchedules wraps the real client (talking to the in-process fake) so a
// test can act after an add lands.
type hookedSchedules struct {
	ScheduleWriter
	afterAdd func()
	delay    time.Duration
}

func (h *hookedSchedules) AddScheduleEvents(ctx context.Context, season, zone string, evs []kumo.ScheduleEvent) error {
	time.Sleep(h.delay)
	if err := h.ScheduleWriter.AddScheduleEvents(ctx, season, zone, evs); err != nil {
		return err
	}
	if h.afterAdd != nil {
		h.afterAdd()
	}
	return nil
}

func scheduleEvents(t *testing.T, st *store.Store) []kumo.ScheduleEvent {
	t.Helper()
	snap, _ := st.Get("office")
	cur, err := kumo.ParseSchedule(snap.Raw["schedule"])
	if err != nil || len(cur.Events) == 0 {
		t.Fatalf("schedule: %v, %d events", err, len(cur.Events))
	}
	return cur.Events
}

// cloudEvents counts the events the fake cloud holds for Office, by re-reading.
func cloudEvents(t *testing.T, r *rig) int {
	t.Helper()
	r.p.PollSchedule(context.Background())
	return len(scheduleEvents(t, r.st))
}

// rowsWithFirstEdited is the current schedule with its first row's heat
// setpoint nudged, as a Save of one edited row would send it.
func rowsWithFirstEdited(evs []kumo.ScheduleEvent) []kumo.ScheduleEvent {
	rows := append([]kumo.ScheduleEvent(nil), evs...)
	h := *rows[0].SpHeat + 0.5
	rows[0].SpHeat = &h
	return rows
}

// A request cancelled between the adds and the deletes must not leave the old
// events behind, nor make the follow-up poll record "context canceled".
func TestApplyScheduleSurvivesCancelledRequest(t *testing.T) {
	r := newRig(t)
	r.p.PollSchedule(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &hookedSchedules{ScheduleWriter: r.client, afterAdd: cancel}
	c := New(Options{Store: r.st, History: r.hist, Schedules: w, AfterSchedule: r.p.PollSchedule})
	before := scheduleEvents(t, r.st)
	ch, err := c.ApplySchedule(ctx, "office", rowsWithFirstEdited(before), "ui")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Added != 1 || ch.Deleted != 1 {
		t.Fatalf("change %+v", ch)
	}
	if n := cloudEvents(t, r); n != len(before) {
		t.Fatalf("%d events, want %d (old event left behind)", n, len(before))
	}
	snap, _ := r.st.Get("office")
	if e := snap.Errors[store.EndpointSchedule]; e != "" {
		t.Fatalf("schedule endpoint error %q", e)
	}
}

// Two overlapping saves of the same edit leave one copy, not two.
func TestApplyScheduleConcurrentSavesDoNotDuplicate(t *testing.T) {
	r := newRig(t)
	r.p.PollSchedule(context.Background())
	w := &hookedSchedules{ScheduleWriter: r.client, delay: 50 * time.Millisecond}
	c := New(Options{Store: r.st, History: r.hist, Schedules: w, AfterSchedule: r.p.PollSchedule})
	before := scheduleEvents(t, r.st)
	rows := rowsWithFirstEdited(before)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); c.ApplySchedule(context.Background(), "office", rows, "ui") }()
	}
	wg.Wait()
	if n := cloudEvents(t, r); n != len(before) {
		t.Fatalf("%d events, want %d", n, len(before))
	}
}
