// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

type rig struct {
	srv    *kumotest.Server
	st     *store.Store
	hist   *history.Log
	p      *poller.Poller
	c      *Controller
	mu     sync.Mutex
	kicks  []string
	chg    []string
	dir    string
	window time.Duration
}

func (r *rig) changes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.chg...)
}

func newRig(t *testing.T) *rig { return newRigWindow(t, 50*time.Millisecond) }

// newRigWindow is newRig with the controller's coalescing window set.
func newRigWindow(t *testing.T, window time.Duration) *rig {
	t.Helper()
	srv := kumotest.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "creds")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	client := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds})
	units, err := poller.Discover(context.Background(), client, filepath.Join(dir, "disc.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(units)
	hist, err := history.Open(filepath.Join(dir, "history.jsonl"), history.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	p := poller.New(client, st, time.Minute)
	r := &rig{srv: srv, st: st, hist: hist, p: p, dir: dir, window: window}
	r.c = r.newController(client)
	st.Subscribe(r.c.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.c.Run(ctx)
	p.PollSlow(ctx)
	p.PollOnce(ctx)
	return r
}

func (r *rig) newController(s Sender) *Controller {
	return New(Options{
		Sender: s, Store: r.st, History: r.hist, Writable: []string{"office"},
		Window: r.window, HoldTimeout: 300 * time.Millisecond,
		LastModePath: filepath.Join(r.dir, "lastmode.json"), SetpointsPath: filepath.Join(r.dir, "setpoints.json"),
		Kick:     func(slug string) { r.mu.Lock(); r.kicks = append(r.kicks, slug); r.mu.Unlock() },
		OnChange: func(slug string) { r.mu.Lock(); r.chg = append(r.chg, slug); r.mu.Unlock() },
	})
}

func f(v float64) *float64 { return &v }
func s(v string) *string   { return &v }

func eventually(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out: " + msg)
}

func TestNotWritable(t *testing.T) {
	r := newRig(t)
	if err := r.c.Apply("house", Intent{SpHeat: f(21)}, "ui"); err != ErrNotWritable {
		t.Fatalf("got %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := len(r.srv.Commands()); n != 0 {
		t.Fatalf("%d commands", n)
	}
}

func TestInvalid(t *testing.T) {
	r := newRig(t)
	if err := r.c.Apply("office", Intent{Mode: s("autoHeat")}, "ui"); err == nil {
		t.Fatal("autoHeat accepted (a state the unit reports, not a mode we send)")
	}
	if err := r.c.Apply("office", Intent{SpCool: f(40)}, "ui"); err == nil {
		t.Fatal("spCool 40 accepted")
	}
	if err := r.c.Apply("office", Intent{SpHeat: f(40)}, "ui"); err == nil {
		t.Fatal("40 accepted")
	}
	if err := r.c.Apply("office", Intent{}, "ui"); err == nil {
		t.Fatal("empty accepted")
	}
	if err := r.c.Apply("office", Intent{SpHeat: f(20.3)}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "command")
	if v := r.srv.Commands()[0].Commands["spHeat"]; v != 20.5 {
		t.Fatalf("sent %v", v)
	}
}

func TestCoalescesBurst(t *testing.T) {
	r := newRigWindow(t, 400*time.Millisecond) // a scheduling stall must not split the burst
	// Back to back: a sleep between them could outlast the 50 ms window on a
	// loaded machine and split the burst.
	for _, v := range []float64{20, 20.5, 21} {
		r.c.Apply("office", Intent{SpHeat: f(v)}, "ui")
	}
	r.c.Apply("office", Intent{Mode: s("heat")}, "ui")
	eventually(t, 2*time.Second, func() bool { return len(r.srv.Commands()) >= 1 }, "sent")
	time.Sleep(600 * time.Millisecond) // any stray second command would show by now
	cmds := r.srv.Commands()
	if len(cmds) != 1 || len(cmds[0].Commands) != 2 || cmds[0].Commands["operationMode"] != "heat" || cmds[0].Commands["spHeat"] != 21.0 {
		t.Fatalf("got %+v", cmds)
	}
}

func TestOverlayShowsPending(t *testing.T) {
	r := newRig(t)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	snap, _ := r.st.Get("office")
	if got := r.c.Overlay(snap).Device.SpHeat; got == nil || *got != 21 {
		t.Fatalf("queued overlay %v", got)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	if got := r.c.Overlay(snap).Device.SpHeat; got == nil || *got != 21 {
		t.Fatalf("sent overlay %v", got)
	}
	if snap.Device.SpHeat != nil && *snap.Device.SpHeat == 21 {
		t.Fatal("overlay mutated the snapshot")
	}
}

func TestHoldConfirmsNumerically(t *testing.T) {
	r := newRig(t)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	r.p.PollOnce(context.Background())
	if p := r.c.Pending("office"); len(p) != 0 {
		t.Fatalf("pending %v", p)
	}
	time.Sleep(400 * time.Millisecond)
	for _, rec := range r.hist.Recent("office", 100) {
		if rec.Endpoint == "unconfirmed" {
			t.Fatalf("unexpected %+v", rec)
		}
	}
}

func TestHoldTimesOut(t *testing.T) {
	r := newRig(t)
	snap, _ := r.st.Get("office")
	old := *snap.Device.SpHeat
	r.c.Apply("office", Intent{SpHeat: f(old + 2)}, "ui")
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	r.srv.SetField(kumotest.OfficeSerial, "spHeat", old)
	r.p.PollOnce(context.Background())
	eventually(t, 2*time.Second, func() bool { return len(r.c.Pending("office")) == 0 }, "hold released")
	// The hold is dropped before the record is appended, so wait for it.
	hasRecord := func() bool {
		for _, rec := range r.hist.Recent("office", 100) {
			if rec.Endpoint == "unconfirmed" {
				return true
			}
		}
		return false
	}
	eventually(t, time.Second, hasRecord, "unconfirmed record")
	var found bool
	for _, rec := range r.hist.Recent("office", 100) {
		if rec.Endpoint == "unconfirmed" && rec.Path == "spHeat" {
			found = true
			if rec.New != jsonOf(old+2) {
				t.Errorf("new %q", rec.New)
			}
			if rec.Old != jsonOf(old) {
				t.Errorf("old %q", rec.Old)
			}
		}
	}
	if !found {
		t.Fatal("no unconfirmed record")
	}
}

func TestSendFailureNoHold(t *testing.T) {
	r := newRig(t)
	r.srv.FailNext("/devices/send-command", 500, 1)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool { return r.c.LastError("office") != "" }, "error")
	if len(r.c.Pending("office")) != 0 || len(r.srv.Commands()) != 0 {
		t.Fatalf("pending %v commands %v", r.c.Pending("office"), r.srv.Commands())
	}
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 && r.c.LastError("office") == "" }, "retry succeeded")
}

func TestCommandRecorded(t *testing.T) {
	r := newRig(t)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool {
		for _, rec := range r.hist.Recent("office", 100) {
			if rec.Endpoint == "command" && rec.Path == "spHeat" && rec.Old == "null" && rec.New == "21" && rec.Source == "ui" {
				return true
			}
		}
		return false
	}, "command record")
}

func TestKickCalled(t *testing.T) {
	r := newRig(t)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.kicks) == 1 && r.kicks[0] == "office" }, "kick")
}

func TestLastModePersists(t *testing.T) {
	r := newRig(t)
	obs := func(mode string) {
		r.c.Observe(store.Update{Slug: "office", Endpoint: store.EndpointDevice, New: []byte(`{"operationMode":"` + mode + `","power":1}`)})
	}
	obs("vent")
	if r.c.LastMode("office") != "vent" {
		t.Fatal(r.c.LastMode("office"))
	}
	obs("off")
	obs("test") // a mode we never offer
	if r.c.LastMode("office") != "vent" {
		t.Fatal(r.c.LastMode("office"))
	}
	c2 := r.newController(nil)
	if c2.LastMode("office") != "vent" {
		t.Fatal(c2.LastMode("office"))
	}
	if c2.LastMode("house") != "heat" {
		t.Fatal("default")
	}
}

func TestOnChangeCalled(t *testing.T) {
	r := newRig(t)
	r.c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	// Queued, then sent: the second call comes after the server saw the command.
	eventually(t, time.Second, func() bool { return len(r.changes()) >= 2 }, "queued and sent notifications")
	n := len(r.changes())
	r.p.PollOnce(context.Background())
	if len(r.changes()) <= n {
		t.Fatal("no change on confirm")
	}
}

func TestNoSendWithoutApply(t *testing.T) {
	r := newRig(t)
	for range 3 {
		r.p.PollOnce(context.Background())
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(r.srv.Commands()); n != 0 {
		t.Fatalf("%d commands", n)
	}
}

func TestPendingMode(t *testing.T) {
	r := newRig(t)
	if _, ok := r.c.PendingMode("office"); ok {
		t.Fatal("pending before apply")
	}
	r.c.Apply("office", Intent{Mode: s("vent")}, "ui")
	if m, ok := r.c.PendingMode("office"); !ok || m != "vent" {
		t.Fatalf("got %q %v", m, ok)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	eventually(t, time.Second, func() bool { _, ok := r.c.PendingMode("office"); return !ok }, "no longer queued once sent")
}

func TestFanAndVaneValidation(t *testing.T) {
	r := newRig(t)
	for _, bad := range []Intent{{Fan: s("turbo")}, {Vane: s("sideways")}} {
		if err := r.c.Apply("office", bad, "ui"); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	if len(r.srv.Commands()) != 0 {
		t.Fatal("invalid intent sent")
	}
	if err := r.c.Apply("office", Intent{Fan: s("superQuiet"), Vane: s("swing")}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "command")
	cmds := r.srv.Commands()[0].Commands
	if cmds["fanSpeed"] != "superQuiet" || cmds["airDirection"] != "swing" {
		t.Fatalf("sent %v", cmds)
	}
}

func TestDryThenCoolWithSetpoint(t *testing.T) {
	r := newRig(t)
	if err := r.c.Apply("office", Intent{Mode: s("dry")}, "api"); err != nil {
		t.Fatal(err)
	}
	if err := r.c.Apply("office", Intent{Mode: s("cool"), SpCool: f(21.2)}, "api"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "command")
	cmds := r.srv.Commands()[0].Commands
	if cmds["operationMode"] != "cool" || cmds["spCool"] != 21.0 && cmds["spCool"] != 21.5 || len(cmds) != 2 {
		t.Fatalf("sent %v", cmds)
	}
}

func autoRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.srv.SetField(kumotest.OfficeSerial, "operationMode", "autoCool")
	r.srv.SetField(kumotest.OfficeSerial, "spCool", 22)
	r.srv.SetField(kumotest.OfficeSerial, "spHeat", 19.5)
	r.p.PollOnce(context.Background())
	return r
}

func TestAutoGapPushesTheOtherSetpoint(t *testing.T) {
	r := autoRig(t)
	if err := r.c.Apply("office", Intent{SpHeat: f(21.5)}, "ui"); err != nil { // 22-21.5 < 1.5
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "command")
	cmds := r.srv.Commands()[0].Commands
	if cmds["spHeat"] != 21.5 || cmds["spCool"] != 23.0 {
		t.Fatalf("sent %v", cmds)
	}
	if err := r.c.Apply("office", Intent{SpCool: f(18)}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 2 }, "second command")
	cmds = r.srv.Commands()[1].Commands
	if cmds["spCool"] != 18.0 || cmds["spHeat"] != 16.5 {
		t.Fatalf("sent %v", cmds)
	}
}

func TestAutoGapPairRefusedAndOtherModesUntouched(t *testing.T) {
	r := autoRig(t)
	if err := r.c.Apply("office", Intent{SpHeat: f(20), SpCool: f(21)}, "ui"); err == nil {
		t.Fatal("pair with a 1.0 gap accepted in auto")
	}
	if err := r.c.Apply("office", Intent{SpHeat: f(31)}, "ui"); err == nil { // would need cool 32.5
		t.Fatal("heat 31 accepted: cool cannot clear it")
	}
	if err := r.c.Apply("office", Intent{Mode: s("heat"), SpHeat: f(21.5)}, "ui"); err != nil {
		t.Fatal("heat setpoint close to cool must be fine outside auto:", err)
	}
}

func TestAutoModeConfirmsAsAutoCool(t *testing.T) {
	r := newRig(t)
	if err := r.c.Apply("office", Intent{Mode: s("auto")}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	r.srv.SetField(kumotest.OfficeSerial, "operationMode", "autoCool") // what the unit reports
	r.p.PollOnce(context.Background())
	eventually(t, time.Second, func() bool { return len(r.c.Pending("office")) == 0 }, "hold released")
	for _, rec := range r.hist.Recent("office", 50) {
		if rec.Endpoint == "unconfirmed" {
			t.Fatalf("auto reported as autoCool must confirm: %+v", rec)
		}
	}
	if r.c.LastMode("office") != "auto" {
		t.Fatal(r.c.LastMode("office"))
	}
}

func TestModeSwitchRestoresLastSetpoints(t *testing.T) {
	r := newRig(t) // office in heat
	// Seen in heat: spHeat 20 is remembered.
	r.c.Observe(store.Update{Slug: "office", Endpoint: store.EndpointDevice, New: []byte(`{"operationMode":"heat","power":1,"spHeat":20,"spCool":26}`)})
	if got := r.c.Remembered("office", "heat"); got.Heat == nil || *got.Heat != 20 {
		t.Fatalf("heat memory %+v", got)
	}
	// We specify an auto band in the UI: remembered for auto.
	if err := r.c.Apply("office", Intent{Mode: s("auto"), SpHeat: f(19), SpCool: f(24)}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "auto sent")
	// The unit rewrites heat on entering auto (the poll that changes family is not recorded).
	r.c.Observe(store.Update{Slug: "office", Endpoint: store.EndpointDevice, New: []byte(`{"operationMode":"autoCool","power":1,"spHeat":17,"spCool":24}`)})
	if got := r.c.Remembered("office", "heat"); *got.Heat != 20 {
		t.Fatalf("heat memory clobbered: %+v", got)
	}
	if got := r.c.Remembered("office", "auto"); *got.Heat != 19 || *got.Cool != 24 {
		t.Fatalf("auto memory %+v (what we specified should stick)", got)
	}
	// Back to heat from the UI with no setpoint: the remembered 20 goes with the mode.
	r.srv.SetField(kumotest.OfficeSerial, "operationMode", "autoCool")
	r.p.PollOnce(context.Background())
	if err := r.c.Apply("office", Intent{Mode: s("heat")}, "ui"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 2 }, "heat sent")
	cmds := r.srv.Commands()[1].Commands
	if cmds["operationMode"] != "heat" || cmds["spHeat"] != 20.0 {
		t.Fatalf("sent %v", cmds)
	}
}

func TestSetpointMemorySurvivesRestart(t *testing.T) {
	r := newRig(t)
	if err := r.c.Apply("office", Intent{SpHeat: f(21)}, "ui"); err != nil { // office is in heat
		t.Fatal(err)
	}
	c2 := r.newController(nil)
	if got := c2.Remembered("office", "heat"); got.Heat == nil || *got.Heat != 21 {
		t.Fatalf("after restart %+v", got)
	}
}

func TestFanChoicesBySpeedCount(t *testing.T) {
	vals := func(n int) string {
		var out []string
		for _, c := range FanChoices(n) {
			out = append(out, c.Value)
		}
		return strings.Join(out, ",")
	}
	if got := vals(3); got != "auto,quiet,low,powerful" {
		t.Errorf("3 speeds: %s", got)
	}
	if got := vals(5); got != "auto,superQuiet,quiet,low,powerful,superPowerful" {
		t.Errorf("5 speeds: %s", got)
	}
	if got := vals(4); got != "" {
		t.Errorf("4 speeds: %s", got)
	}
}

func TestApplyRejectsModeTheProfileLacks(t *testing.T) {
	r := newRig(t)
	snap, _ := r.st.Get("office")
	var ms []map[string]any
	if err := json.Unmarshal(snap.Raw["profile"], &ms); err != nil {
		t.Fatal(err)
	}
	ms[0]["hasModeDry"] = false
	raw, _ := json.Marshal(ms)
	if err := r.st.Put("office", "profile", raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.c.Apply("office", Intent{Mode: s("dry")}, "ui"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dry accepted without hasModeDry: %v", err)
	}
	if err := r.c.Apply("office", Intent{Mode: s("off")}, "ui"); err != nil {
		t.Fatal(err)
	}
}

// A unit whose status has been read but whose profile has not must refuse a
// cool setpoint, not panic.
func TestSpCoolWithoutProfile(t *testing.T) {
	st := store.New([]store.Unit{{Slug: "office", Serial: "x"}})
	if err := st.Put("office", store.EndpointStatus, []byte(`{"minSetPoint":16,"maxSetPoint":31}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Store: st, Writable: []string{"office"}})
	if err := c.Apply("office", Intent{SpCool: f(24)}, "ui"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

// Apply checks each setpoint against the unit's limits for the mode it is
// for. With House's profile alone (heat 10–28, cool 19–30, auto 19–28) a low
// cool setpoint must not pass on the heat range; with its status range
// (19.5–28) as well, that narrower range holds for every mode.
func TestApplyPerModeRanges(t *testing.T) {
	prof, err := os.ReadFile("../../pkg/kumo/kumotest/fixtures/profile_house.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mode   string // the unit's current mode
		in     Intent
		ok     bool
		status bool // also store House's status range
	}{
		{"heat low, profile only", "heat", Intent{SpHeat: f(12)}, true, false},
		{"heat low, status says 19.5", "heat", Intent{SpHeat: f(12)}, false, true},
		{"heat above max", "heat", Intent{SpHeat: f(28.5)}, false, false},
		{"cool high, profile only", "cool", Intent{SpCool: f(30)}, true, false},
		{"cool high, status says 28", "cool", Intent{SpCool: f(30)}, false, true},
		{"cool below cool min (heat range would allow it)", "cool", Intent{SpCool: f(18)}, false, false},
		{"switch to cool with cool setpoint", "heat", Intent{Mode: s("cool"), SpCool: f(29.5)}, true, false},
		{"auto below auto min", "auto", Intent{SpHeat: f(18)}, false, false},
		{"auto heat pushes cool past auto max", "auto", Intent{SpHeat: f(27)}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New([]store.Unit{{Slug: "house", Serial: "x"}})
			now := time.Now()
			if err := st.Put("house", store.EndpointProfile, prof, now); err != nil {
				t.Fatal(err)
			}
			if tc.status {
				if err := st.Put("house", store.EndpointStatus, []byte(`{"minSetPoint":19.5,"maxSetPoint":28}`), now); err != nil {
					t.Fatal(err)
				}
			}
			dev := `{"operationMode":"` + tc.mode + `","power":1,"spHeat":20,"spCool":25}`
			if err := st.Put("house", store.EndpointDevice, []byte(dev), now); err != nil {
				t.Fatal(err)
			}
			c := New(Options{Store: st, Writable: []string{"house"}})
			err := c.Apply("house", tc.in, "ui")
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

type spySender struct {
	inner Sender
	saw   func() bool
	mu    sync.Mutex
	seen  []bool
}

func (s *spySender) SendCommand(ctx context.Context, serial string, cmds map[string]any) error {
	s.mu.Lock()
	s.seen = append(s.seen, s.saw())
	s.mu.Unlock()
	return s.inner.SendCommand(ctx, serial, cmds)
}

func TestLastSentIsSetBeforeTheCommandCall(t *testing.T) {
	r := newRig(t)
	c := r.c
	sp := &spySender{inner: c.o.Sender}
	c.o.Sender = sp
	sp.saw = func() bool { return !c.LastSent("office").IsZero() }
	c.Apply("office", Intent{SpHeat: f(21)}, "ui")
	eventually(t, time.Second, func() bool { return len(r.srv.Commands()) == 1 }, "sent")
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if len(sp.seen) != 1 || !sp.seen[0] {
		t.Fatalf("LastSent set before the send: %v", sp.seen)
	}
}
