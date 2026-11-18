// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/internal/control"
)

// Arc is one wedge of a dial.
type Arc struct {
	ui.Choice
	Path string
	Lit  bool // filled up to the current wedge (fan level), as well as the current one
	// Label position, just outside the ring.
	LX, LY float64
	Anchor string
}

// dialSplit separates the wedge choices from the side buttons (the choices
// whose value is in extra, e.g. auto and swing).
func dialSplit(opts []ui.Choice, extra ...string) (wedges, buttons []ui.Choice) {
	for _, o := range opts {
		isExtra := false
		for _, e := range extra {
			if o.Value == e {
				isExtra = true
			}
		}
		if isExtra {
			buttons = append(buttons, o)
		} else {
			wedges = append(wedges, o)
		}
	}
	return
}

// dialArcs lays the wedges on a 300° ring with the gap at the bottom, first
// at bottom-left running clockwise. With cumulative set, every wedge up to
// the pressed one is lit (a level); otherwise only the pressed one. The
// bottom choices (e.g. swing) share the gap at the bottom.
func dialArcs(wedges []ui.Choice, cumulative bool, bottom []ui.Choice) []Arc {
	const (
		cx, cy       = 90.0, 70.0
		outer, inner = 46.0, 28.0
		start, sweep = 210.0, 300.0
		gap          = 3.0
	)
	pt := func(r, deg float64) (float64, float64) {
		a := deg * math.Pi / 180
		return cx + r*math.Sin(a), cy - r*math.Cos(a)
	}
	arc := func(w ui.Choice, a0, a1 float64, lit bool) Arc {
		a0, a1 = a0+gap/2, a1-gap/2
		ox0, oy0 := pt(outer, a0)
		ox1, oy1 := pt(outer, a1)
		ix1, iy1 := pt(inner, a1)
		ix0, iy0 := pt(inner, a0)
		d := fmt.Sprintf("M%.2f %.2fA%.0f %.0f 0 0 1 %.2f %.2fL%.2f %.2fA%.0f %.0f 0 0 0 %.2f %.2fZ",
			ox0, oy0, outer, outer, ox1, oy1, ix1, iy1, inner, inner, ix0, iy0)
		mid := (a0 + a1) / 2
		anchor, off := "middle", 10.0 // top and bottom labels need more room
		if sn := math.Sin(mid * math.Pi / 180); sn > 0.3 {
			anchor, off = "start", 5
		} else if sn < -0.3 {
			anchor, off = "end", 5
		}
		lx, ly := pt(outer+off, mid)
		return Arc{Choice: w, Path: d, Lit: lit, LX: lx, LY: ly, Anchor: anchor}
	}
	cur := -1
	for i, w := range wedges {
		if w.Pressed {
			cur = i
		}
	}
	n := float64(len(wedges))
	var out []Arc
	for i, w := range wedges {
		out = append(out, arc(w, start+sweep*float64(i)/n, start+sweep*float64(i+1)/n, cumulative && i <= cur))
	}
	m := float64(len(bottom))
	for i, w := range bottom {
		g0 := start + sweep // 150°, where the ring ends
		out = append(out, arc(w, g0+(360-sweep)*float64(i)/m, g0+(360-sweep)*float64(i+1)/m, false))
	}
	return out
}

// dialCenter is the text in the middle: the pressed choice's label, or a dash;
// a wheel with an Auto hub always says Auto there (the lit wedge shows the value).
func dialCenter(opts []ui.Choice, fixed bool, hub ui.Choice) string {
	if fixed {
		return hub.Label
	}
	for _, o := range opts {
		if o.Pressed {
			return o.Label
		}
	}
	return "–"
}

// dialHub picks the choice with the given value out of the side buttons to be the clickable
// hub of the ring; the rest stay as buttons.
func dialHub(buttons []ui.Choice, value string) (hub ui.Choice, ok bool, rest []ui.Choice) {
	for _, b := range buttons {
		if b.Value == value {
			hub, ok = b, true
		} else {
			rest = append(rest, b)
		}
	}
	return
}

// Handle is one draggable setpoint on the temperature wheel.
type Handle struct {
	Field, Path string // control field (pending marker) and UI endpoint
	Label       string
	V           float64
	X, Y        float64
	Pending     bool
}

// TempWheel is the geometry for the temperature wheel; dial.js redraws the
// same shapes while dragging.
type TempWheel struct {
	Min, Max               float64
	Handles                []Handle
	Track, Fill            string
	Band                   bool // two handles: the fill is the band between them
	Centre                 string
	MinLabel, MaxLabel     string
	MinX, MinY, MaxX, MaxY float64
	Kind                   string // heat, cool or band, for the fill colour
	Title                  string
	NA                     bool // the mode has no target: an inert ring reading N/A
}

const (
	tCX, tCY, tR = 90.0, 70.0, 37.0
	tGap         = control.MinAutoGap // the unit's minimum auto gap
)

func fnum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func tAngle(w *TempWheel, v float64) float64 { return 210 + 300*(v-w.Min)/(w.Max-w.Min) }

func tPoint(r, deg float64) (float64, float64) {
	a := deg * math.Pi / 180
	return tCX + r*math.Sin(a), tCY - r*math.Cos(a)
}

func tArc(w *TempWheel, v0, v1 float64) string {
	a0, a1 := tAngle(w, v0), tAngle(w, v1)
	if a1 <= a0 {
		return ""
	}
	x0, y0 := tPoint(tR, a0)
	x1, y1 := tPoint(tR, a1)
	large := 0
	if a1-a0 > 180 {
		large = 1
	}
	return fmt.Sprintf("M%.2f %.2fA%.0f %.0f 0 %d 1 %.2f %.2f", x0, y0, tR, tR, large, x1, y1)
}

// tempWheel builds the wheel for a card, or nil when the limits are unknown.
// A mode without a setpoint (fan only, off) gets an inert N/A ring so the
// layout does not jump.
func tempWheel(c ui.CardView) *TempWheel {
	lo, e1 := strconv.ParseFloat(c.SpMin, 64)
	hi, e2 := strconv.ParseFloat(c.SpMax, 64)
	if e1 != nil || e2 != nil || hi <= lo {
		return nil
	}
	w := &TempWheel{Min: lo, Max: hi}
	if !c.ShowHeat && !c.ShowCool {
		w.NA, w.Kind, w.Title, w.Centre = true, "na", "TARGET", "N/A"
		w.Track = tArc(w, lo, hi)
		return w
	}
	add := func(field, path, label, val string) {
		v, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return
		}
		v = math.Min(hi, math.Max(lo, v))
		x, y := tPoint(tR, tAngle(w, v))
		w.Handles = append(w.Handles, Handle{Field: field, Path: path, Label: label, V: v, X: x, Y: y, Pending: c.Control.Pending[field] != ""})
	}
	if c.ShowHeat {
		add("spHeat", "spheat", "Heat to", c.SpHeat)
	}
	if c.ShowCool {
		add("spCool", "spcool", "Cool to", c.SpCool)
	}
	if len(w.Handles) == 0 {
		return nil
	}
	w.Track = tArc(w, lo, hi)
	w.Band = len(w.Handles) == 2
	switch {
	case w.Band:
		w.Kind, w.Title = "band", "TARGET"
		w.Fill = tArc(w, w.Handles[0].V, w.Handles[1].V)
	case c.ShowHeat:
		w.Kind, w.Title = "heat", "TARGET"
		w.Fill = tArc(w, lo, w.Handles[0].V)
	default:
		w.Kind, w.Title = "cool", "TARGET"
		w.Fill = tArc(w, lo, w.Handles[0].V)
	}
	parts := make([]string, len(w.Handles))
	for i, h := range w.Handles {
		parts[i] = fnum(h.V)
	}
	w.Centre = strings.Join(parts, " – ")
	w.MinLabel, w.MaxLabel = fnum(lo), fnum(hi)
	w.MinX, w.MinY = tPoint(tR, 210)
	w.MaxX, w.MaxY = tPoint(tR, 150)
	w.MinY += 13
	w.MaxY += 13
	return w
}

// modeOpts is the mode choices plus the Off hub.
func modeOpts(c ui.CardView) []ui.Choice {
	return append(append([]ui.Choice{}, c.Modes...), ui.Choice{Value: "off", Label: "Off", Pressed: !c.On})
}

// tvalY is the baseline of the centre text: higher when a "°C" sits under it.
func tvalY(w *TempWheel) string {
	if w.NA {
		return "70"
	}
	return "66"
}

// holdSignal makes a slug safe inside a datastar signal name.
func holdSignal(slug string) string { return strings.NewReplacer("-", "_", ".", "_").Replace(slug) }

func b2s(b bool) string { return strconv.FormatBool(b) }

// hideUnless is the initial style of an element datastar shows later.
func hideUnless(show bool) string {
	if show {
		return ""
	}
	return "display:none"
}

// holdClick is the Hold button's handler: end a hold, else start one until
// the next event, else just open the options.
func holdClick(on, canNext bool, open string, pick func(string) string) string {
	switch {
	case on:
		return open + " = false; " + pick("none")
	case canNext:
		return pick("next")
	}
	return open + " = !" + open
}

// pendingTitle is a control's tooltip: its label, with "pending" while the
// unit has not yet reported the value (it goes live once the unit does).
func pendingTitle(label string, pending bool) string {
	if pending {
		return label + " (pending: waiting for the unit)"
	}
	return label
}
