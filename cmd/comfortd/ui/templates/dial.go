// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"fmt"
	"math"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
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

// dialCenter is the text in the middle: the pressed choice's label, or a dash.
func dialCenter(opts []ui.Choice) string {
	for _, o := range opts {
		if o.Pressed {
			return o.Label
		}
	}
	return "–"
}

// dialHub picks the "auto" choice out of the side buttons to be the clickable
// hub of the ring; the rest stay as buttons.
func dialHub(buttons []ui.Choice) (hub ui.Choice, ok bool, rest []ui.Choice) {
	for _, b := range buttons {
		if b.Value == "auto" {
			hub, ok = b, true
		} else {
			rest = append(rest, b)
		}
	}
	return
}
