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
// the pressed one is lit (a level); otherwise only the pressed one.
func dialArcs(wedges []ui.Choice, cumulative bool) []Arc {
	const (
		cx, cy       = 90.0, 60.0
		outer, inner = 46.0, 28.0
		start, sweep = 210.0, 300.0
		gap          = 3.0
	)
	pt := func(r, deg float64) (float64, float64) {
		a := deg * math.Pi / 180
		return cx + r*math.Sin(a), cy - r*math.Cos(a)
	}
	cur := -1
	for i, w := range wedges {
		if w.Pressed {
			cur = i
		}
	}
	n := float64(len(wedges))
	out := make([]Arc, len(wedges))
	for i, w := range wedges {
		a0 := start + sweep*float64(i)/n + gap/2
		a1 := start + sweep*float64(i+1)/n - gap/2
		ox0, oy0 := pt(outer, a0)
		ox1, oy1 := pt(outer, a1)
		ix1, iy1 := pt(inner, a1)
		ix0, iy0 := pt(inner, a0)
		d := fmt.Sprintf("M%.2f %.2fA%.0f %.0f 0 0 1 %.2f %.2fL%.2f %.2fA%.0f %.0f 0 0 0 %.2f %.2fZ",
			ox0, oy0, outer, outer, ox1, oy1, ix1, iy1, inner, inner, ix0, iy0)
		mid := (a0 + a1) / 2
		lx, ly := pt(outer+5, mid)
		anchor := "middle"
		if sn := math.Sin(mid * math.Pi / 180); sn > 0.3 {
			anchor = "start"
		} else if sn < -0.3 {
			anchor = "end"
		}
		out[i] = Arc{Choice: w, Path: d, Lit: cumulative && i <= cur, LX: lx, LY: ly, Anchor: anchor}
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
