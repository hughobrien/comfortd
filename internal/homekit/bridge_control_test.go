// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

type ctlRig struct {
	srv *kumotest.Server
	st  *store.Store
	p   *poller.Poller
	ctl *control.Controller
	b   *Bridge
}

// newCtlRig builds a bridge for both units on a real controller and the fake
// cloud. office starts in the given mode (power follows).
func newCtlRig(t *testing.T, mode string) *ctlRig {
	t.Helper()
	srv := kumotest.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "c")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	client := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds})
	units, err := poller.Discover(context.Background(), client, filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetField(kumotest.OfficeSerial, "operationMode", mode)
	if mode == "off" {
		srv.SetField(kumotest.OfficeSerial, "power", 0)
	} else {
		srv.SetField(kumotest.OfficeSerial, "power", 1)
	}
	st := store.New(units)
	p := poller.New(client, st, time.Minute)
	p.PollSlow(context.Background())
	p.PollOnce(context.Background())
	var b *Bridge
	ctl := control.New(control.Options{
		Sender: client, Store: st, Writable: []string{"office"},
		Window: 50 * time.Millisecond, HoldTimeout: time.Second,
		LastModePath: filepath.Join(dir, "lastmode.json"),
		OnChange:     func(slug string) { b.Sync(slug) },
	})
	st.Subscribe(ctl.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ctl.Run(ctx)
	b, err = New(Options{
		StateDir: dir, Version: "test", Snapshot: st.Get, Interval: time.Minute, Units: units, Control: ctl,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &ctlRig{srv, st, p, ctl, b}
}

func (r *ctlRig) waitCommands(t *testing.T, n int) []kumotest.Command {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.srv.Commands()) >= n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // let any extra command show up
	cmds := r.srv.Commands()
	if len(cmds) != n {
		t.Fatalf("want %d commands, got %+v", n, cmds)
	}
	return cmds
}

func TestActiveZeroSendsOff(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetActive("office", 0); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if len(cmds[0].Commands) != 1 || cmds[0].Commands["operationMode"] != "off" {
		t.Fatalf("got %+v", cmds)
	}
}

func TestActiveOnFromOffUsesLastMode(t *testing.T) {
	r := newCtlRig(t, "vent")
	r.p.PollOnce(context.Background()) // Observe records vent as last mode
	r.srv.SetField(kumotest.OfficeSerial, "operationMode", "off")
	r.srv.SetField(kumotest.OfficeSerial, "power", 0)
	r.p.PollOnce(context.Background())
	if err := r.b.onSetActive("office", 1); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if cmds[0].Commands["operationMode"] != "vent" || len(cmds[0].Commands) != 1 {
		t.Fatalf("got %+v", cmds)
	}
}

func offAfterVent(t *testing.T) *ctlRig {
	t.Helper()
	r := newCtlRig(t, "vent")
	r.p.PollOnce(context.Background())
	r.srv.SetField(kumotest.OfficeSerial, "operationMode", "off")
	r.srv.SetField(kumotest.OfficeSerial, "power", 0)
	r.p.PollOnce(context.Background())
	return r
}

func TestActiveAfterTargetKeepsHeat(t *testing.T) {
	r := offAfterVent(t)
	if err := r.b.onSetTarget("office", TargetHeat); err != nil {
		t.Fatal(err)
	}
	if err := r.b.onSetActive("office", 1); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if cmds[0].Commands["operationMode"] != "heat" {
		t.Fatalf("got %+v", cmds)
	}
}

func TestTargetAfterActiveIsHeat(t *testing.T) {
	r := offAfterVent(t)
	if err := r.b.onSetActive("office", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.b.onSetTarget("office", TargetHeat); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if cmds[0].Commands["operationMode"] != "heat" {
		t.Fatalf("got %+v", cmds)
	}
}

func TestActiveOnWhenAlreadyOnNoop(t *testing.T) {
	r := newCtlRig(t, "vent")
	if err := r.b.onSetActive("office", 1); err != nil {
		t.Fatal(err)
	}
	r.waitCommands(t, 0)
}

func TestSliderBurstOneCommand(t *testing.T) {
	r := newCtlRig(t, "heat")
	for _, v := range []float64{19.5, 20, 20.5, 21} {
		if err := r.b.onSetHeatThresh("office", v); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cmds := r.waitCommands(t, 1)
	if len(cmds[0].Commands) != 1 || cmds[0].Commands["spHeat"] != 21.0 {
		t.Fatalf("got %+v", cmds)
	}
}

func TestSyncUsesOverlay(t *testing.T) {
	r := newCtlRig(t, "heat")
	r.srv.SetField(kumotest.OfficeSerial, "spHeat", 19)
	r.p.PollOnce(context.Background())
	if err := r.b.onSetHeatThresh("office", 21); err != nil {
		t.Fatal(err)
	}
	// OnChange synced already; the poll has not returned 21 yet.
	if v := r.b.by["office"].thresh.Value(); v != 21 {
		t.Fatalf("threshold %v, want pending 21", v)
	}
}

func TestNotWritableStillRejected(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetActive("house", 0); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("got %v", err)
	}
	if err := r.b.onSetHeatThresh("house", 21); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("got %v", err)
	}
	r.waitCommands(t, 0)
}

func TestThresholdOutOfRange(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetHeatThresh("office", 45); !errors.Is(err, control.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	r.waitCommands(t, 0)
}
