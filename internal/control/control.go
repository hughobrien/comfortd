// SPDX-License-Identifier: GPL-3.0-or-later

// Package control is the only code that sends commands to the cloud. It
// validates intents, coalesces bursts into one command, holds the requested
// values as pending until a poll confirms them, and records what it did.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hughobrien/comfortd/internal/fsutil"
	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

var (
	ErrNotWritable = errors.New("control: unit not writable")
	// ErrInvalid is wrapped with the reason.
	ErrInvalid = errors.New("control: invalid intent")
)

// allowedModes are the modes Apply accepts. Extended per feature.
var allowedModes = []string{"off", "heat", "vent"}

const (
	fieldMode   = "operationMode"
	fieldSpHeat = "spHeat"
	fieldFan    = "fanSpeed"
	fieldVane   = "airDirection"
	sendTimeout = 30 * time.Second
)

type Intent struct {
	Mode   *string
	SpHeat *float64
	Fan    *string // "auto" or a speed name from FanSpeeds
	Vane   *string // a direction from vaneDirections, or "swing"
}

// Choice is one selectable value: the API value and the Comfort app's label.
type Choice struct{ Value, Label string }

// ModeChoices are the modes the UI offers (off is the power button).
func ModeChoices() []Choice {
	return []Choice{{"heat", "Heat"}, {"vent", "Fan only"}}
}

// FanChoices returns the fan speeds for a unit with n speeds, slowest first,
// after auto. Names and labels are comfort_HA's table (API value → app label);
// the 5-speed set is verified by probe on Office (api-inventory). The 3-speed
// mapping (House) is not known, so none are offered.
func FanChoices(n int) []Choice {
	if n != 5 {
		return nil
	}
	return []Choice{{"auto", "Auto"}, {"superQuiet", "Quiet"}, {"quiet", "Low"}, {"low", "Medium"}, {"powerful", "High"}, {"superPowerful", "Powerful"}}
}

// VaneChoices returns the vane positions the profile supports.
func VaneChoices(p kumo.Profile) []Choice {
	var out []Choice
	if p.HasVaneDir {
		out = append(out, Choice{"auto", "Auto"})
	}
	if p.HasVaneSwing {
		out = append(out, Choice{"swing", "Swing"})
	}
	if p.HasVaneDir {
		out = append(out, Choice{"vertical", "Lowest"}, Choice{"midvertical", "Low"}, Choice{"midpoint", "Middle"}, Choice{"midhorizontal", "High"}, Choice{"horizontal", "Highest"})
	}
	return out
}

func hasValue(cs []Choice, v string) bool {
	return slices.ContainsFunc(cs, func(c Choice) bool { return c.Value == v })
}

type Sender interface {
	SendCommand(ctx context.Context, serial string, commands map[string]any) error
}

type Options struct {
	Sender       Sender
	Store        *store.Store
	History      *history.Log
	Kick         func(slug string)
	Writable     []string
	Window       time.Duration // coalescing window, default 1s
	HoldTimeout  time.Duration // pending hold, default 30s
	LastModePath string
	OnChange     func(slug string)
	Now          func() time.Time
}

type field struct {
	val     any
	sent    bool
	expires time.Time // when a sent field gives up waiting for confirmation
}

type unitState struct {
	fields   map[string]*field
	source   string
	deadline time.Time // when queued (unsent) fields are flushed
	lastErr  string
}

type Controller struct {
	o    Options
	wake chan struct{}

	mu       sync.Mutex
	units    map[string]*unitState
	lastMode map[string]string
}

func New(o Options) *Controller {
	if o.Window == 0 {
		o.Window = time.Second
	}
	if o.HoldTimeout == 0 {
		o.HoldTimeout = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	c := &Controller{o: o, wake: make(chan struct{}, 1), units: map[string]*unitState{}, lastMode: map[string]string{}}
	if o.LastModePath != "" {
		if b, err := os.ReadFile(o.LastModePath); err == nil {
			json.Unmarshal(b, &c.lastMode)
		}
	}
	return c
}

func (c *Controller) Writable(slug string) bool { return slices.Contains(c.o.Writable, slug) }

func (c *Controller) changed(slug string) {
	if c.o.OnChange != nil {
		c.o.OnChange(slug)
	}
}

func (c *Controller) state(slug string) *unitState {
	st := c.units[slug]
	if st == nil {
		st = &unitState{fields: map[string]*field{}}
		c.units[slug] = st
	}
	return st
}

// Apply validates and queues an intent; it returns immediately.
func (c *Controller) Apply(slug string, in Intent, source string) error {
	if !c.Writable(slug) {
		return ErrNotWritable
	}
	if in.Mode == nil && in.SpHeat == nil && in.Fan == nil && in.Vane == nil {
		return fmt.Errorf("%w: nothing to change", ErrInvalid)
	}
	var prof *kumo.Profile
	if in.Fan != nil || in.Vane != nil {
		snap, ok := c.o.Store.Get(slug)
		if !ok || snap.Profile == nil {
			return fmt.Errorf("%w: capabilities unknown", ErrInvalid)
		}
		prof = snap.Profile
	}
	if in.Fan != nil && !hasValue(FanChoices(prof.NumberOfFanSpeeds), *in.Fan) {
		return fmt.Errorf("%w: fan speed %q not supported", ErrInvalid, *in.Fan)
	}
	if in.Vane != nil {
		if !hasValue(VaneChoices(*prof), *in.Vane) {
			return fmt.Errorf("%w: air direction %q not supported", ErrInvalid, *in.Vane)
		}
	}
	var mode *string
	var sp *float64
	if in.Mode != nil {
		if !slices.Contains(allowedModes, *in.Mode) {
			return fmt.Errorf("%w: mode %q not allowed", ErrInvalid, *in.Mode)
		}
		mode = in.Mode
	}
	if in.SpHeat != nil {
		snap, ok := c.o.Store.Get(slug)
		lo, hi, hasRange := snap.SetpointRange()
		if !ok || !hasRange {
			return fmt.Errorf("%w: setpoint range unknown", ErrInvalid)
		}
		v := math.Round(*in.SpHeat*2) / 2
		if math.IsNaN(v) || v < lo || v > hi {
			return fmt.Errorf("%w: spHeat %v outside %v–%v", ErrInvalid, *in.SpHeat, lo, hi)
		}
		sp = &v
	}
	c.mu.Lock()
	st := c.state(slug)
	if mode != nil {
		st.fields[fieldMode] = &field{val: *mode}
	}
	if sp != nil {
		st.fields[fieldSpHeat] = &field{val: *sp}
	}
	if in.Fan != nil {
		st.fields[fieldFan] = &field{val: *in.Fan}
	}
	if in.Vane != nil {
		st.fields[fieldVane] = &field{val: *in.Vane}
	}
	st.source = source
	st.deadline = c.o.Now().Add(c.o.Window)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	c.changed(slug)
	return nil
}

// Pending returns the requested values not yet confirmed by a poll.
func (c *Controller) Pending(slug string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{}
	if st := c.units[slug]; st != nil {
		for k, f := range st.fields {
			out[k] = f.val
		}
	}
	return out
}

func (c *Controller) LastError(slug string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.units[slug]; st != nil {
		return st.lastErr
	}
	return ""
}

// LastMode is the last non-off allowed mode seen on the unit, default heat.
func (c *Controller) LastMode(slug string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.lastMode[slug]; m != "" {
		return m
	}
	return "heat"
}

// PendingMode returns a mode that is queued but not yet sent.
func (c *Controller) PendingMode(slug string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.units[slug]; st != nil {
		if f := st.fields[fieldMode]; f != nil && !f.sent {
			m, _ := f.val.(string)
			return m, true
		}
	}
	return "", false
}

// Overlay returns s with pending values in place of the polled ones.
func (c *Controller) Overlay(s store.Snapshot) store.Snapshot {
	if s.Device == nil {
		return s
	}
	pend := c.Pending(s.Unit.Slug)
	if len(pend) == 0 {
		return s
	}
	d := *s.Device
	if m, ok := pend[fieldMode].(string); ok {
		d.OperationMode = m
		if m == "off" {
			d.Power = 0
		} else {
			d.Power = 1
		}
	}
	if v, ok := pend[fieldSpHeat].(float64); ok {
		d.SpHeat = &v
	}
	if v, ok := pend[fieldFan].(string); ok {
		d.FanSpeed = v
	}
	if v, ok := pend[fieldVane].(string); ok {
		d.AirDirection = v
	}
	s.Device = &d
	return s
}

// Observe is a store subscriber: it confirms holds and tracks the last mode.
func (c *Controller) Observe(u store.Update) {
	if u.Endpoint != store.EndpointDevice {
		return
	}
	d, err := kumo.ParseDevice(u.New)
	if err != nil {
		return
	}
	confirmed := false
	c.mu.Lock()
	if st := c.units[u.Slug]; st != nil {
		for name, f := range st.fields {
			if f.sent && matches(name, f.val, d) {
				delete(st.fields, name)
				confirmed = true
			}
		}
	}
	saveMode := false
	if d.OperationMode != "off" && slices.Contains(allowedModes, d.OperationMode) && c.lastMode[u.Slug] != d.OperationMode {
		c.lastMode[u.Slug] = d.OperationMode
		saveMode = true
	}
	var modes []byte
	if saveMode && c.o.LastModePath != "" {
		modes, _ = json.Marshal(c.lastMode)
	}
	c.mu.Unlock()
	if modes != nil {
		if err := fsutil.WriteFileAtomic(c.o.LastModePath, modes); err != nil {
			slog.Warn("control: save last mode", "err", err)
		}
	}
	if confirmed {
		c.changed(u.Slug)
	}
}

func matches(name string, val any, d kumo.Device) bool {
	switch name {
	case fieldMode:
		return d.OperationMode == val
	case fieldSpHeat:
		v, _ := val.(float64)
		return d.SpHeat != nil && *d.SpHeat == v
	case fieldFan:
		return d.FanSpeed == val
	case fieldVane:
		return d.AirDirection == val
	}
	return false
}

func polledJSON(name string, d *kumo.Device) string {
	if d == nil {
		return "null"
	}
	switch name {
	case fieldMode:
		b, _ := json.Marshal(d.OperationMode)
		return string(b)
	case fieldSpHeat:
		if d.SpHeat != nil {
			return strconv.FormatFloat(*d.SpHeat, 'f', -1, 64)
		}
	case fieldFan:
		b, _ := json.Marshal(d.FanSpeed)
		return string(b)
	case fieldVane:
		b, _ := json.Marshal(d.AirDirection)
		return string(b)
	}
	return "null"
}

// Run flushes queued intents and expires holds until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	for {
		var tc <-chan time.Time
		var timer *time.Timer
		if next, ok := c.nextEvent(); ok {
			timer = time.NewTimer(max(0, next.Sub(c.o.Now())))
			tc = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-c.wake:
		case <-tc:
		}
		if timer != nil {
			timer.Stop()
		}
		c.tick(ctx)
	}
}

func (c *Controller) nextEvent() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var next time.Time
	consider := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, st := range c.units {
		unsent := false
		for _, f := range st.fields {
			if f.sent {
				consider(f.expires)
			} else {
				unsent = true
			}
		}
		if unsent {
			consider(st.deadline)
		}
	}
	return next, !next.IsZero()
}

type flush struct {
	slug   string
	cmds   map[string]any
	source string
}

type timeout struct {
	slug, name string
	val        any
}

func (c *Controller) tick(ctx context.Context) {
	now := c.o.Now()
	var sends []flush
	var timeouts []timeout
	c.mu.Lock()
	for slug, st := range c.units {
		cmds := map[string]any{}
		for name, f := range st.fields {
			switch {
			case f.sent && !now.Before(f.expires):
				timeouts = append(timeouts, timeout{slug, name, f.val})
				delete(st.fields, name)
			case !f.sent && !now.Before(st.deadline):
				cmds[name] = f.val
			}
		}
		if len(cmds) > 0 {
			for name := range cmds {
				st.fields[name].sent = true
				st.fields[name].expires = now.Add(c.o.HoldTimeout)
			}
			sends = append(sends, flush{slug, cmds, st.source})
		}
	}
	c.mu.Unlock()

	for _, t := range timeouts {
		snap, _ := c.o.Store.Get(t.slug)
		c.record(t.slug, "unconfirmed", t.name, polledJSON(t.name, snap.Device), jsonOf(t.val), "")
		c.changed(t.slug)
	}
	for _, s := range sends {
		c.send(ctx, s)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (c *Controller) record(slug, endpoint, path, old, new, source string) {
	if c.o.History == nil {
		return
	}
	r := history.Record{T: c.o.Now().UTC(), Unit: slug, Endpoint: endpoint, Path: path, Old: old, New: new, Source: source}
	if err := c.o.History.Append(r); err != nil {
		slog.Warn("control: record history", "err", err)
	}
}

func (c *Controller) send(ctx context.Context, s flush) {
	snap, ok := c.o.Store.Get(s.slug)
	if !ok {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	err := c.o.Sender.SendCommand(sctx, snap.Unit.Serial, s.cmds)
	c.mu.Lock()
	st := c.state(s.slug)
	if err != nil {
		for name, v := range s.cmds {
			if f := st.fields[name]; f != nil && f.sent && f.val == v {
				delete(st.fields, name)
			}
		}
		st.lastErr = err.Error()
	} else {
		st.lastErr = ""
	}
	c.mu.Unlock()
	if err != nil {
		slog.Warn("control: send failed", "unit", s.slug, "err", err)
		c.changed(s.slug)
		return
	}
	names := make([]string, 0, len(s.cmds))
	for name := range s.cmds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		c.record(s.slug, "command", name, "null", jsonOf(s.cmds[name]), s.source)
	}
	if c.o.Kick != nil {
		c.o.Kick(s.slug)
	}
	c.changed(s.slug)
}
