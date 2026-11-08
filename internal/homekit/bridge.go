// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/store"
)

// Controller is the write path (internal/control.Controller).
type Controller interface {
	Apply(slug string, in control.Intent, source string) error
	Overlay(s store.Snapshot) store.Snapshot
	Writable(slug string) bool
	LastMode(slug string) string
	PendingMode(slug string) (string, bool)
}

// ErrReadOnly is returned for every write from Home in this version.
var ErrReadOnly = errors.New("comfortd: read-only in this version")

// statusCommFailure is HAP's "communication failure", shown as "No Response".
const statusCommFailure = hap.JsonStatusServiceCommunicationFailure

const (
	defaultHeatMin = 10.0
	defaultHeatMax = 31.0
)

// allowedTargets is the set of TargetHeaterCoolerState values we accept
// until cool/auto are probed.
var allowedTargets = []int{TargetHeat}

type Options struct {
	StateDir, BridgeName, Version string
	Port                          int
	Units                         []store.Unit // only the units to expose
	Snapshot                      func(slug string) (store.Snapshot, bool)
	Interval                      time.Duration // poll interval, for staleness
	Now                           func() time.Time
	ForceStale                    map[string]bool
	Control                       Controller // nil = read-only everywhere
}

type AccessoryInfo struct {
	Slug, Name, Serial string
	AID                uint64
	Stale              bool
}

type unitAcc struct {
	unit   store.Unit
	aid    uint64
	acc    *accessory.A
	hc     *service.HeaterCooler
	thresh *characteristic.HeatingThresholdTemperature
}

type Bridge struct {
	Pin  string
	Name string

	o      Options
	server *hap.Server
	units  []*unitAcc
	by     map[string]*unitAcc
	mu     sync.Mutex
}

func New(o Options) (*Bridge, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.BridgeName == "" {
		o.BridgeName = "comfortd"
	}
	serials := make([]string, len(o.Units))
	for i, u := range o.Units {
		serials[i] = u.Serial
	}
	aids, err := AssignAIDs(filepath.Join(o.StateDir, "aids.json"), serials)
	if err != nil {
		return nil, fmt.Errorf("homekit: aids: %w", err)
	}
	pin, err := LoadOrGeneratePin(o.StateDir)
	if err != nil {
		return nil, fmt.Errorf("homekit: pin: %w", err)
	}
	b := &Bridge{Pin: pin, Name: o.BridgeName, o: o, by: map[string]*unitAcc{}}
	var children []*accessory.A
	for _, u := range o.Units {
		ua := b.buildUnit(u, aids[u.Serial])
		b.units = append(b.units, ua)
		b.by[u.Slug] = ua
		children = append(children, ua.acc)
	}
	bridgeAcc := accessory.NewBridge(accessory.Info{
		Name: o.BridgeName, Manufacturer: "comfortd", Model: "comfortd", SerialNumber: o.BridgeName, Firmware: o.Version,
	})
	srv, err := hap.NewServer(hap.NewFsStore(filepath.Join(o.StateDir, "hap")), bridgeAcc.A, children...)
	if err != nil {
		return nil, fmt.Errorf("homekit: server: %w", err)
	}
	srv.Pin = pin
	srv.Addr = ":" + strconv.Itoa(o.Port)
	b.server = srv
	for _, ua := range b.units {
		b.Sync(ua.unit.Slug)
	}
	return b, nil
}

func (b *Bridge) buildUnit(u store.Unit, aid uint64) *unitAcc {
	model := "Kumo"
	if snap, ok := b.o.Snapshot(u.Slug); ok && snap.Device != nil && snap.Device.ModelNumber != "" {
		model = snap.Device.ModelNumber
	}
	acc := accessory.New(accessory.Info{
		Name: u.Name, Manufacturer: "Mitsubishi", Model: model, SerialNumber: u.Serial, Firmware: b.o.Version,
	}, accessory.TypeAirConditioner)
	acc.Id = aid
	hc := service.NewHeaterCooler()
	thresh := characteristic.NewHeatingThresholdTemperature()
	thresh.SetMinValue(defaultHeatMin)
	thresh.SetMaxValue(defaultHeatMax)
	thresh.SetStepValue(0.5)
	thresh.SetValue(defaultHeatMin)
	hc.AddC(thresh.C)
	hc.TargetHeaterCoolerState.ValidVals = allowedTargets
	hc.TargetHeaterCoolerState.SetValue(TargetHeat)
	hc.CurrentTemperature.SetMinValue(-20)
	hc.CurrentTemperature.SetMaxValue(60)
	hc.CurrentTemperature.SetStepValue(0.5)
	acc.AddS(hc.S)

	ua := &unitAcc{unit: u, aid: aid, acc: acc, hc: hc, thresh: thresh}
	slug := u.Slug
	hc.Active.OnSetRemoteValue(func(v int) error { return b.onSetActive(slug, v) })
	hc.TargetHeaterCoolerState.OnSetRemoteValue(func(v int) error { return b.onSetTarget(slug, v) })
	thresh.OnSetRemoteValue(func(v float64) error { return b.onSetHeatThresh(slug, v) })
	for _, c := range []*characteristic.C{hc.Active.C, hc.CurrentHeaterCoolerState.C, hc.TargetHeaterCoolerState.C, hc.CurrentTemperature.C, thresh.C} {
		c.ValueRequestFunc = b.valueRequest(slug, c)
	}
	return ua
}

// writable reports whether Home may write to slug.
func (b *Bridge) writable(slug string) bool {
	return b.o.Control != nil && b.o.Control.Writable(slug)
}

func (b *Bridge) onSetActive(slug string, v int) error {
	if !b.writable(slug) {
		return ErrReadOnly
	}
	ctl := b.o.Control
	if v == 0 {
		off := "off"
		return ctl.Apply(slug, control.Intent{Mode: &off}, "homekit")
	}
	// A mode already queued in this window (e.g. Target Heat) wins.
	if _, queued := ctl.PendingMode(slug); queued {
		return nil
	}
	if snap, ok := b.o.Snapshot(slug); ok && snap.Device != nil {
		if d := ctl.Overlay(snap).Device; d.Power == 1 && d.OperationMode != "off" {
			return nil // already on, possibly in a mode we don't map to Heat
		}
	}
	mode := ctl.LastMode(slug)
	return ctl.Apply(slug, control.Intent{Mode: &mode}, "homekit")
}

func (b *Bridge) onSetTarget(slug string, v int) error {
	if !b.writable(slug) {
		return ErrReadOnly
	}
	if v != TargetHeat {
		return fmt.Errorf("%w: target state %d", control.ErrInvalid, v)
	}
	heat := "heat"
	return b.o.Control.Apply(slug, control.Intent{Mode: &heat}, "homekit")
}

func (b *Bridge) onSetHeatThresh(slug string, v float64) error {
	if !b.writable(slug) {
		return ErrReadOnly
	}
	return b.o.Control.Apply(slug, control.Intent{SpHeat: &v}, "homekit")
}

func (b *Bridge) valueRequest(slug string, c *characteristic.C) func(*http.Request) (interface{}, int) {
	return func(*http.Request) (interface{}, int) {
		if b.stale(slug) {
			return c.Value(), statusCommFailure
		}
		return c.Value(), 0
	}
}

func (b *Bridge) stale(slug string) bool {
	if b.o.ForceStale[slug] {
		return true
	}
	snap, ok := b.o.Snapshot(slug)
	if !ok || snap.Device == nil {
		return true
	}
	return snap.Stale(b.o.Now(), b.o.Interval)
}

// Sync re-reads the unit's snapshot and updates its characteristics.
func (b *Bridge) Sync(slug string) {
	ua, ok := b.by[slug]
	if !ok {
		return
	}
	snap, ok := b.o.Snapshot(slug)
	if !ok || snap.Device == nil {
		return
	}
	if b.o.Control != nil {
		snap = b.o.Control.Overlay(snap)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	lo, hi := defaultHeatMin, defaultHeatMax
	if l, h, ok := snap.SetpointRange(); ok {
		lo, hi = l, h
	}
	st := FromDevice(*snap.Device, allowedTargets)
	if st.HeatThresh != nil {
		// The real value must show even if it is outside the advertised range.
		lo, hi = min(lo, *st.HeatThresh), max(hi, *st.HeatThresh)
	}
	ua.thresh.SetMinValue(lo)
	ua.thresh.SetMaxValue(hi)
	if st.HeatThresh != nil {
		ua.thresh.SetValue(*st.HeatThresh)
	}
	if st.CurrentTemp != nil {
		ua.hc.CurrentTemperature.SetValue(*st.CurrentTemp)
	}
	ua.hc.Active.SetValue(st.Active)
	ua.hc.CurrentHeaterCoolerState.SetValue(st.Current)
	if st.Target != nil {
		ua.hc.TargetHeaterCoolerState.SetValue(*st.Target)
	}
}

// Run serves HAP until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) error { return b.server.ListenAndServe(ctx) }

func (b *Bridge) Accessories() []AccessoryInfo {
	out := make([]AccessoryInfo, len(b.units))
	for i, ua := range b.units {
		out[i] = AccessoryInfo{Slug: ua.unit.Slug, Name: ua.unit.Name, Serial: ua.unit.Serial, AID: ua.aid, Stale: b.stale(ua.unit.Slug)}
	}
	return out
}
