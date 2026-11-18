// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

type holdCall struct {
	op         string
	site, zone string
	until      *time.Time
}

type fakeHolds struct {
	calls []holdCall
	away  []kumo.AwayHold
	err   error
}

func (f *fakeHolds) SetHold(_ context.Context, site, zone string, until *time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, holdCall{"set", site, zone, until})
	return nil
}

func (f *fakeHolds) EndHold(_ context.Context, site, zone string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, holdCall{"end", site, zone, nil})
	return nil
}

func (f *fakeHolds) SetAway(_ context.Context, site, zone string, a kumo.AwayHold, until *time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.away = append(f.away, a)
	f.calls = append(f.calls, holdCall{"away", site, zone, until})
	return nil
}

func (f *fakeHolds) EndAway(_ context.Context, site, zone string, a kumo.AwayHold) error {
	if f.err != nil {
		return f.err
	}
	f.away = append(f.away, a)
	f.calls = append(f.calls, holdCall{"endaway", site, zone, nil})
	return nil
}

// holdCtl is a controller on the rig's store with fake hold writes. Office is writable.
func holdCtl(r *rig, h HoldWriter, writable ...string) *Controller {
	return New(Options{Store: r.st, History: r.hist, Writable: writable, Holds: h,
		LastModePath: filepath.Join(r.dir, "lm.json"), SetpointsPath: filepath.Join(r.dir, "sp.json"),
		Kick:      func(slug string) { r.mu.Lock(); r.kicks = append(r.kicks, slug); r.mu.Unlock() },
		AfterHold: func(context.Context) { r.mu.Lock(); r.kicks = append(r.kicks, "zone"); r.mu.Unlock() },
		Now:       func() time.Time { return time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC) }})
}

func (r *rig) holdRecords() (n int) {
	for _, rec := range r.hist.Recent("office", 100) {
		if rec.Endpoint == "hold" {
			n++
		}
	}
	return
}

func TestSetAndEndHold(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{}
	c := holdCtl(r, fh, "office")
	until := time.Date(2026, 11, 17, 6, 22, 0, 0, time.UTC)
	if err := c.SetHold(context.Background(), "office", &until, "ui"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetHold(context.Background(), "office", nil, "ui"); err != nil {
		t.Fatal(err)
	}
	if err := c.EndHold(context.Background(), "office", "ui"); err != nil {
		t.Fatal(err)
	}
	if len(fh.calls) != 3 || fh.calls[0].site != kumotest.SiteID || fh.calls[0].zone != kumotest.OfficeZoneID ||
		fh.calls[0].until == nil || !fh.calls[0].until.Equal(until) || fh.calls[1].until != nil || fh.calls[2].op != "end" {
		t.Fatalf("calls %+v", fh.calls)
	}
	if n := r.holdRecords(); n != 3 {
		t.Fatalf("history records for hold: %d", n)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.kicks) != 6 { // a device kick and a zone refresh per write
		t.Fatalf("kicks %v", r.kicks)
	}
}

func TestHoldNotWritable(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{}
	until := time.Date(2026, 11, 17, 6, 22, 0, 0, time.UTC)
	for _, c := range []*Controller{holdCtl(r, fh, "office"), holdCtl(r, nil, "house"), holdCtl(r, nil, "office", "house")} {
		slug := "house"
		if c.o.Holds == nil {
			slug = "office"
		}
		if err := c.SetHold(context.Background(), slug, &until, "ui"); !errors.Is(err, ErrNotWritable) {
			t.Fatalf("%s: %v", slug, err)
		}
		if err := c.EndHold(context.Background(), slug, "ui"); !errors.Is(err, ErrNotWritable) {
			t.Fatalf("%s end: %v", slug, err)
		}
	}
	if len(fh.calls) != 0 || r.holdRecords() != 0 {
		t.Fatal("something was sent or recorded")
	}
}

func TestHoldPastTime(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{}
	c := holdCtl(r, fh, "office")
	past := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	if err := c.SetHold(context.Background(), "office", &past, "ui"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if err := c.SetHold(context.Background(), "nope", &past, "ui"); !errors.Is(err, ErrNotWritable) && !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown unit: %v", err)
	}
	if len(fh.calls) != 0 {
		t.Fatal("sent")
	}
}

func TestHoldWriteFails(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{err: errors.New("boom")}
	c := holdCtl(r, fh, "office")
	if err := c.EndHold(context.Background(), "office", "ui"); err == nil {
		t.Fatal("want the error")
	}
	if r.holdRecords() != 0 {
		t.Fatal("a failed write was recorded")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.kicks) != 0 {
		t.Fatalf("kicked after a failure: %v", r.kicks)
	}
}

// spyHolds checks, from inside the cloud call, that LastSent is already set:
// the push echo can arrive before the HTTP response does.
type spyHolds struct {
	fakeHolds
	sawSet func() bool
	saw    []bool
}

func (s *spyHolds) SetHold(ctx context.Context, site, zone string, until *time.Time) error {
	s.saw = append(s.saw, s.sawSet())
	return s.fakeHolds.SetHold(ctx, site, zone, until)
}

func (s *spyHolds) EndHold(ctx context.Context, site, zone string) error {
	s.saw = append(s.saw, s.sawSet())
	return s.fakeHolds.EndHold(ctx, site, zone)
}

func TestLastSentIsSetBeforeTheHoldCall(t *testing.T) {
	r := newRig(t)
	sp := &spyHolds{}
	c := holdCtl(r, sp, "office")
	sp.sawSet = func() bool { return !c.LastSent("office").IsZero() }
	if !c.LastSent("office").IsZero() {
		t.Fatal("LastSent should start zero")
	}
	until := time.Date(2026, 11, 17, 6, 22, 0, 0, time.UTC)
	if err := c.SetHold(context.Background(), "office", &until, "ui"); err != nil {
		t.Fatal(err)
	}
	if err := c.EndHold(context.Background(), "office", "ui"); err != nil {
		t.Fatal(err)
	}
	if len(sp.saw) != 2 || !sp.saw[0] || !sp.saw[1] {
		t.Fatalf("LastSent set before the call: %v", sp.saw)
	}
}

func TestSetAndEndAway(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{}
	c := holdCtl(r, fh, "office")
	until := time.Date(2026, 11, 17, 6, 22, 0, 0, time.UTC)
	if err := c.SetAway(context.Background(), "office", &until, "ui"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAway(context.Background(), "office", nil, "ui"); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	if err := c.SetAway(context.Background(), "office", &past, "ui"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("past end: %v", err)
	}
	if fh.calls[0].op != "away" || fh.calls[0].until == nil || !fh.calls[0].until.Equal(until) || fh.calls[1].until != nil || len(fh.calls) != 2 {
		t.Fatalf("calls %+v", fh.calls)
	}
	a := fh.away[0]
	if a.OperationMode == "" || a.SpHeat >= a.SpCool || a.SpHeat < 17 || a.SpCool > 26 || a.FanSpeed != "auto" {
		t.Fatalf("away settings %+v", a)
	}
	if n := r.holdRecords(); n != 2 {
		t.Fatalf("history records for away: %d", n)
	}
}

func TestEndHoldOfAwayEndsTheAway(t *testing.T) {
	r := newRig(t)
	fh := &fakeHolds{}
	c := holdCtl(r, fh, "office")
	r.srv.SetHoldMode(kumotest.OfficeZoneID, true, "away")
	r.p.PollOnce(context.Background())
	if err := c.EndHold(context.Background(), "office", "ui"); err != nil {
		t.Fatal(err)
	}
	if len(fh.calls) != 1 || fh.calls[0].op != "endaway" {
		t.Fatalf("calls %+v", fh.calls)
	}
}

func TestAwaySettingsFollowTheUnitsLimits(t *testing.T) {
	r := newRig(t)
	s, _ := r.st.Get("house")
	a := AwaySettings(s)
	if lo, _, ok := s.SetpointRange(a.OperationMode); ok && a.SpHeat < lo {
		t.Fatalf("away heat %v below the unit's floor %v", a.SpHeat, lo)
	}
}
