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
		Sender: client, Store: st,
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

func TestThresholdOutOfRange(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetHeatThresh("office", 45); !errors.Is(err, control.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	r.waitCommands(t, 0)
}

func TestSpeedAndSwingSendCommands(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetSpeed("office", 100); err != nil {
		t.Fatal(err)
	}
	if err := r.b.onSetSwing("office", 1); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if cmds[0].Commands["fanSpeed"] != "superPowerful" || cmds[0].Commands["airDirection"] != "swing" {
		t.Fatalf("got %+v", cmds)
	}
	if err := r.b.onSetSwing("office", 0); err != nil {
		t.Fatal(err)
	}
	cmds = r.waitCommands(t, 2)
	if cmds[1].Commands["airDirection"] != "auto" {
		t.Fatalf("got %+v", cmds)
	}
}

func TestTargetStatesAndCoolThreshold(t *testing.T) {
	r := newCtlRig(t, "heat")
	for target, mode := range map[int]string{TargetCool: "cool", TargetAuto: "auto"} {
		n := len(r.srv.Commands())
		if err := r.b.onSetTarget("office", target); err != nil {
			t.Fatal(err)
		}
		cmds := r.waitCommands(t, n+1)
		if cmds[n].Commands["operationMode"] != mode {
			t.Fatalf("target %d sent %+v", target, cmds[n])
		}
	}
	if err := r.b.onSetTarget("office", 7); err == nil {
		t.Fatal("bogus target accepted")
	}
	n := len(r.srv.Commands())
	if err := r.b.onSetCoolThresh("office", 24); err != nil {
		t.Fatal(err)
	}
	if cmds := r.waitCommands(t, n+1); cmds[n].Commands["spCool"] != 24.0 {
		t.Fatalf("sent %+v", cmds[n])
	}
}

func TestModeSwitches(t *testing.T) {
	r := newCtlRig(t, "heat")
	if err := r.b.onSetModeSwitch("office", "dry", true); err != nil {
		t.Fatal(err)
	}
	cmds := r.waitCommands(t, 1)
	if cmds[0].Commands["operationMode"] != "dry" {
		t.Fatalf("got %+v", cmds)
	}
	r.p.PollOnce(context.Background())
	if !r.b.by["office"].dry.On.Value() || r.b.by["office"].fan.On.Value() {
		t.Fatal("dry switch should be on, fan off")
	}
	// Off for a mode the unit is not in does nothing.
	if err := r.b.onSetModeSwitch("office", "vent", false); err != nil {
		t.Fatal(err)
	}
	r.waitCommands(t, 1)
	// Off for the current mode turns the unit off.
	if err := r.b.onSetModeSwitch("office", "dry", false); err != nil {
		t.Fatal(err)
	}
	cmds = r.waitCommands(t, 2)
	if cmds[1].Commands["operationMode"] != "off" {
		t.Fatalf("got %+v", cmds)
	}
}
