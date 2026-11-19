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
	ErrUnavailable = errors.New("control: not available")
	// ErrInvalid is wrapped with the reason.
	ErrInvalid = errors.New("control: invalid intent")
)

// allowedModes are the modes Apply accepts. Extended per feature.
var allowedModes = []string{"off", "heat", "vent", "dry", "cool", "auto"}

// MinAutoGap is the smallest spCool-spHeat the unit keeps in auto; it moves
// the other setpoint to hold it (observed on Office).
const MinAutoGap = 1.5

// ModeFamily folds the unit's auto sub-states (autoCool, autoHeat) into auto.
func ModeFamily(m string) string {
	if m == "autoCool" || m == "autoHeat" {
		return "auto"
	}
	return m
}

const (
	fieldMode   = "operationMode"
	fieldSpHeat = "spHeat"
	fieldSpCool = "spCool"
	// FieldSpHeat and FieldSpCool name the setpoint fields for RangeKey.
	FieldSpHeat = fieldSpHeat
	FieldSpCool = fieldSpCool
	fieldFan    = "fanSpeed"
	fieldVane   = "airDirection"
	sendTimeout = 30 * time.Second
)

type Intent struct {
	Mode   *string
	SpHeat *float64
	SpCool *float64
	Fan    *string // "auto" or a speed name from FanSpeeds
	Vane   *string // a direction from vaneDirections, or "swing"
}

// Choice is one selectable value: the API value and the Comfort app's label.
type Choice struct{ Value, Label string }

// ModeChoices are the modes the UI offers for a unit (off is the power
// button), in the Comfort app's order. Cool and auto are detected from the
// profile's setpoint limits; the profile has no flags for them.
func ModeChoices(p kumo.Profile) []Choice {
	var out []Choice
	if p.HasModeVent {
		out = append(out, Choice{"vent", "Fan"})
	}
	if p.HasModeHeat {
		out = append(out, Choice{"heat", "Heat"})
	}
	if p.SupportsCool() {
		out = append(out, Choice{"cool", "Cool"})
	}
	if p.HasModeDry {
		out = append(out, Choice{"dry", "Dry"})
	}
	if p.SupportsAuto() {
		out = append(out, Choice{"auto", "Auto"})
	}
	return out
}

// FanChoices returns the fan speeds for a unit with n speeds, slowest first,
// after auto. Names and labels are comfort_HA's table (API value → app label);
// the 5-speed set is verified on Office and the 3-speed set on House
// (api-inventory); other counts offer none.
func FanChoices(n int) []Choice {
	switch n {
	case 3:
		return []Choice{{"auto", "Auto"}, {"quiet", "Low"}, {"low", "Medium"}, {"powerful", "High"}}
	case 5:
	default:
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
	Sender        Sender
	Schedules     ScheduleWriter            // nil = schedule edits are off
	Holds         HoldWriter                // nil = hold changes are off
	Prefs         PrefsClient               // the account preference behind the limits feature; nil = unknown
	Limits        LimitWriter               // nil = limit changes are off
	AfterLimits   func(ctx context.Context) // refetch the status after a limit change
	AfterSchedule func(ctx context.Context) // refetch the schedules after an edit
	AfterHold     func(ctx context.Context) // refetch the zones after a hold change (the hold lives in the zone view)
	Store         *store.Store
	History       *history.Log
	Kick          func(slug string)
	Window        time.Duration // coalescing window, default 1s
	HoldTimeout   time.Duration // pending hold, default 30s
	LastModePath  string
	SetpointsPath string // remembered setpoints per mode, persisted
	OnChange      func(slug string)
	Now           func() time.Time
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

	mu                      sync.Mutex
	units                   map[string]*unitState
	limitsOn, limitsOnKnown bool                 // the account's Min/Max setpoints preference, as last read
	lastSent                map[string]time.Time // when we last sent this unit a command or hold, for attributing pushes
	lastMode                map[string]string
	lastFam                 map[string]string               // mode family seen on the previous poll
	mem                     map[string]map[string]Setpoints // slug -> mode family -> setpoints last seen in it
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
	c := &Controller{o: o, wake: make(chan struct{}, 1), units: map[string]*unitState{}, lastMode: map[string]string{},
		lastFam: map[string]string{}, mem: map[string]map[string]Setpoints{}}
	if o.LastModePath != "" {
		if b, err := os.ReadFile(o.LastModePath); err == nil {
			json.Unmarshal(b, &c.lastMode)
		}
	}
	if o.SetpointsPath != "" {
		if b, err := os.ReadFile(o.SetpointsPath); err == nil {
			json.Unmarshal(b, &c.mem)
		}
	}
	return c
}

// Setpoints are the setpoints a mode last had; nil = not known.
type Setpoints struct {
	Heat *float64 `json:"heat,omitempty"`
	Cool *float64 `json:"cool,omitempty"`
}

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
	if in.Mode == nil && in.SpHeat == nil && in.SpCool == nil && in.Fan == nil && in.Vane == nil {
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
		if snap, ok := c.o.Store.Get(slug); ok && snap.Profile != nil && *in.Mode != "off" && !hasValue(ModeChoices(*snap.Profile), *in.Mode) {
			return fmt.Errorf("%w: mode %q not supported by this unit", ErrInvalid, *in.Mode)
		}
		mode = in.Mode
	}
	fam := c.effectiveFamily(slug, mode)
	if in.SpHeat != nil {
		snap, ok := c.o.Store.Get(slug)
		lo, hi, hasRange := snap.SetpointRange(RangeKey(fieldSpHeat, fam))
		if !ok || !hasRange {
			return fmt.Errorf("%w: setpoint range unknown", ErrInvalid)
		}
		v := math.Round(*in.SpHeat*2) / 2
		if math.IsNaN(v) || v < lo || v > hi {
			return fmt.Errorf("%w: spHeat %v outside %v–%v", ErrInvalid, *in.SpHeat, lo, hi)
		}
		sp = &v
	}
	var spc *float64
	if in.SpCool != nil {
		snap, ok := c.o.Store.Get(slug)
		lo, hi, hasRange := snap.SetpointRange(RangeKey(fieldSpCool, fam))
		if !ok || !hasRange || snap.Profile == nil || !snap.Profile.SupportsCool() {
			return fmt.Errorf("%w: unit has no cool setpoint range", ErrInvalid)
		}
		v := math.Round(*in.SpCool*2) / 2
		if math.IsNaN(v) || v < lo || v > hi {
			return fmt.Errorf("%w: spCool %v outside %v–%v", ErrInvalid, *in.SpCool, lo, hi)
		}
		spc = &v
	}
	if mode != nil && in.SpHeat == nil && in.SpCool == nil {
		c.recall(slug, *mode, &sp, &spc)
	}
	if err := c.holdAutoGap(slug, mode, &sp, &spc); err != nil {
		return err
	}
	c.noteSpecified(slug, mode, sp, spc)
	c.mu.Lock()
	st := c.state(slug)
	if sp != nil {
		st.fields[fieldSpHeat] = &field{val: *sp}
	}
	if spc != nil {
		st.fields[fieldSpCool] = &field{val: *spc}
	}
	if mode != nil {
		st.fields[fieldMode] = &field{val: *mode}
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

// noteSpecified remembers setpoints we are about to send under the mode they
// are for (the mode in the intent, else the unit's current one): what the user
// last asked for is what that mode should come back with.
func (c *Controller) noteSpecified(slug string, mode *string, sp, spc *float64) {
	if sp == nil && spc == nil {
		return
	}
	snap, ok := c.o.Store.Get(slug)
	if !ok || snap.Device == nil {
		return
	}
	fam := ModeFamily(c.Overlay(snap).Device.OperationMode)
	if mode != nil {
		fam = ModeFamily(*mode)
	}
	c.mu.Lock()
	have := c.mem[slug][fam]
	switch fam {
	case "heat":
		if sp != nil {
			have.Heat = sp
		}
	case "cool", "dry":
		if spc != nil {
			have.Cool = spc
		}
	case "auto":
		if sp != nil {
			have.Heat = sp
		}
		if spc != nil {
			have.Cool = spc
		}
	default:
		c.mu.Unlock()
		return
	}
	if c.mem[slug] == nil {
		c.mem[slug] = map[string]Setpoints{}
	}
	c.mem[slug][fam] = have
	var b []byte
	if c.o.SetpointsPath != "" {
		b, _ = json.Marshal(c.mem)
	}
	c.mu.Unlock()
	if b != nil {
		if err := fsutil.WriteFileAtomic(c.o.SetpointsPath, b); err != nil {
			slog.Warn("control: save setpoints", "err", err)
		}
	}
}

// recall fills in the setpoints the mode last had, so switching mode does not
// leave the unit's own choice (it rewrites setpoints on a mode change). Values
// outside the unit's current range are ignored.
func (c *Controller) recall(slug, mode string, sp, spc **float64) {
	fam := ModeFamily(mode)
	mem := c.Remembered(slug, fam)
	snap, ok := c.o.Store.Get(slug)
	if !ok {
		return
	}
	inRange := func(field string, v *float64) *float64 {
		lo, hi, hasRange := snap.SetpointRange(RangeKey(field, fam))
		if v == nil || !hasRange || *v < lo || *v > hi {
			return nil
		}
		return v
	}
	switch fam {
	case "heat":
		*sp = inRange(fieldSpHeat, mem.Heat)
	case "cool", "dry":
		*spc = inRange(fieldSpCool, mem.Cool)
	case "auto":
		h, cl := inRange(fieldSpHeat, mem.Heat), inRange(fieldSpCool, mem.Cool)
		if h != nil && cl != nil && *cl-*h >= MinAutoGap {
			*sp, *spc = h, cl
		}
	}
}

// RangeKey is the profile limit key for a setpoint field in a mode family:
// auto has its own limits for both setpoints; otherwise spCool follows the
// cool limits and spHeat the heat ones.
func RangeKey(field, fam string) string {
	switch {
	case fam == "auto":
		return "auto"
	case field == fieldSpCool:
		return "cool"
	}
	return "heat"
}

// effectiveFamily is the mode family an intent's setpoints are for: the mode
// in the intent, else the unit's current one (pending included).
func (c *Controller) effectiveFamily(slug string, mode *string) string {
	if mode != nil {
		return ModeFamily(*mode)
	}
	if snap, ok := c.o.Store.Get(slug); ok && snap.Device != nil {
		return ModeFamily(c.Overlay(snap).Device.OperationMode)
	}
	return ""
}

// holdAutoGap keeps spCool-spHeat >= MinAutoGap when the unit is (or is going
// to be) in auto: a lone setpoint pushes the other one, as the unit would; a
// pair that breaks the gap is refused.
func (c *Controller) holdAutoGap(slug string, mode *string, sp, spc **float64) error {
	snap, ok := c.o.Store.Get(slug)
	if !ok || snap.Device == nil {
		return nil
	}
	cur := c.Overlay(snap).Device
	eff := ModeFamily(cur.OperationMode)
	if mode != nil {
		eff = ModeFamily(*mode)
	}
	if eff != "auto" || (*sp == nil && *spc == nil) {
		return nil
	}
	h, cl := cur.SpHeat, cur.SpCool
	if *sp != nil {
		h = *sp
	}
	if *spc != nil {
		cl = *spc
	}
	if h == nil || cl == nil || *cl-*h >= MinAutoGap {
		return nil
	}
	lo, hi, _ := snap.SetpointRange("auto")
	switch {
	case *sp != nil && *spc != nil:
		return fmt.Errorf("%w: auto needs the cool setpoint at least %v above heat", ErrInvalid, MinAutoGap)
	case *sp != nil:
		v := *h + MinAutoGap
		if v > hi {
			return fmt.Errorf("%w: auto needs cool at least %v above heat, but cool tops out at %v", ErrInvalid, MinAutoGap, hi)
		}
		*spc = &v
	default:
		v := *cl - MinAutoGap
		if v < lo {
			return fmt.Errorf("%w: auto needs heat at least %v below cool, but heat bottoms out at %v", ErrInvalid, MinAutoGap, lo)
		}
		*sp = &v
	}
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
	if v, ok := pend[fieldSpCool].(float64); ok {
		d.SpCool = &v
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
			if f.sent && matches(name, f.val, d) && unitReported(name, f.val, d) {
				delete(st.fields, name)
				confirmed = true
			}
		}
	}
	saveMode := false
	if fam := ModeFamily(d.OperationMode); fam != "off" && slices.Contains(allowedModes, fam) && c.lastMode[u.Slug] != fam {
		c.lastMode[u.Slug] = fam
		saveMode = true
	}
	var sps []byte
	if c.rememberSetpoints(u.Slug, d) && c.o.SetpointsPath != "" {
		sps, _ = json.Marshal(c.mem)
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
	if sps != nil {
		if err := fsutil.WriteFileAtomic(c.o.SetpointsPath, sps); err != nil {
			slog.Warn("control: save setpoints", "err", err)
		}
	}
	if confirmed {
		c.changed(u.Slug)
	}
}

// rememberSetpoints (c.mu held) files the device's setpoints under its mode
// family: heat keeps spHeat, cool keeps spCool, auto keeps both. The poll on
// which the family changes is skipped, because the unit rewrites setpoints on
// a mode change (entering auto overwrote spHeat) and that is not the mode's
// own value. It reports whether anything changed.
func (c *Controller) rememberSetpoints(slug string, d kumo.Device) bool {
	fam := ModeFamily(d.OperationMode)
	prev, seen := c.lastFam[slug]
	c.lastFam[slug] = fam
	if seen && prev != fam {
		return false
	}
	var want Setpoints
	switch fam {
	case "heat":
		want.Heat = d.SpHeat
	case "cool", "dry": // dry uses the cool setpoint (the app shows spCool in dry)
		want.Cool = d.SpCool
	case "auto":
		if d.SpHeat == nil || d.SpCool == nil {
			return false
		}
		want.Heat, want.Cool = d.SpHeat, d.SpCool
	default:
		return false
	}
	have := c.mem[slug][fam]
	if equalPtr(have.Heat, want.Heat) && equalPtr(have.Cool, want.Cool) {
		return false
	}
	if c.mem[slug] == nil {
		c.mem[slug] = map[string]Setpoints{}
	}
	c.mem[slug][fam] = want
	return true
}

func equalPtr(a, b *float64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// Remembered returns the setpoints last seen in a mode family.
func (c *Controller) Remembered(slug, family string) Setpoints {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mem[slug][family]
}

func matches(name string, val any, d kumo.Device) bool {
	switch name {
	case fieldMode:
		return d.OperationMode == val || (val == "auto" && ModeFamily(d.OperationMode) == "auto")
	case fieldSpHeat:
		v, _ := val.(float64)
		return d.SpHeat != nil && *d.SpHeat == v
	case fieldSpCool:
		v, _ := val.(float64)
		return d.SpCool != nil && *d.SpCool == v
	case fieldFan:
		return d.FanSpeed == val
	case fieldVane:
		return d.AirDirection == val
	}
	return false
}

// unitReported is true when the unit itself has reported the value, not just
// the cloud. The device record's realValues holds what the unit last reported,
// present only while it differs from the requested top-level value (observed on
// spHeat and spCool: the old value shows for a few seconds after a command,
// then the entry goes). A field is "live" once it matches and has no differing
// realValues entry; until then it is "pending".
func unitReported(name string, val any, d kumo.Device) bool {
	rv, ok := d.RealValues[name]
	if !ok {
		return true
	}
	if n, isNum := rv.(float64); isNum {
		v, _ := val.(float64)
		return n == v
	}
	return rv == val
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
	case fieldSpCool:
		if d.SpCool != nil {
			return strconv.FormatFloat(*d.SpCool, 'f', -1, 64)
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

// LastSent is when comfortd last sent slug a command or hold request (zero if
// never). It is set before the request goes out, because the cloud's push echo
// can arrive before the HTTP response does.
func (c *Controller) LastSent(slug string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSent[slug]
}

func (c *Controller) markSent(slug string) {
	c.mu.Lock()
	if c.lastSent == nil {
		c.lastSent = map[string]time.Time{}
	}
	c.lastSent[slug] = c.o.Now()
	c.mu.Unlock()
}

func (c *Controller) send(ctx context.Context, s flush) {
	snap, ok := c.o.Store.Get(s.slug)
	if !ok {
		return
	}
	c.markSent(s.slug)
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
