// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
)

func TestUnitSectionRenders(t *testing.T) {
	v := ui.UnitView{Slug: "office", Name: "Office", Endpoints: []ui.EndpointView{
		{Name: "device", Rows: []ui.Row{{Path: "operationMode", Value: `"heat"`, Changed: true}}},
	}}
	var b bytes.Buffer
	if err := UnitSection(v).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{`id="unit-office"`, "operationMode", "changed", "&#34;heat&#34;"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}

func TestCardStaleRendered(t *testing.T) {
	var b bytes.Buffer
	v := ui.UnitView{Slug: "office", Name: "Office", Card: ui.CardView{Stale: true, LastOK: "12:00:00"}}
	if err := UnitSection(v).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "stale") || !strings.Contains(b.String(), "12:00:00") {
		t.Fatal(b.String())
	}
}

func TestCardPendingMarked(t *testing.T) {
	var sb strings.Builder
	c := ui.CardView{Slug: "office", Mode: "Heat", SpHeat: "19", SpMin: "10", SpMax: "31", On: true, ShowHeat: true,
		Modes:   []ui.Choice{{Value: "heat", Label: "Heat", Pressed: true}, {Value: "vent", Label: "Fan"}},
		Control: ui.CardControl{Writable: true, Pending: map[string]string{"spHeat": "19.5"}}}
	if err := Card(c).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, `class="handle pending"`) {
		t.Fatalf("setpoint not marked pending: %s", out)
	}
	if strings.Count(out, `aria-pressed="true"`) != 1 { // current mode only; Off is not pressed while on
		t.Fatalf("want only Heat pressed: %s", out)
	}
	if strings.Contains(out, `class="pending"`) {
		t.Fatalf("mode wasn't pending: %s", out)
	}
	if !strings.Contains(out, `data-handle`) || !strings.Contains(out, `data-min="10"`) {
		t.Fatalf("temperature wheel missing: %s", out)
	}
}

func TestCardFanOnlyShowsNA(t *testing.T) {
	var sb strings.Builder
	c := ui.CardView{Slug: "office", Mode: "Fan", SpHeat: "19", SpCool: "22", SpMin: "10", SpMax: "31", On: true,
		Modes:   []ui.Choice{{Value: "heat", Label: "Heat"}, {Value: "vent", Label: "Fan", Pressed: true}},
		Control: ui.CardControl{Writable: true}}
	if err := Card(c).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "N/A") || !strings.Contains(out, "TARGET") {
		t.Fatalf("want an N/A target ring: %s", out)
	}
	if strings.Contains(out, "data-handle") || strings.Contains(out, `class="fill`) || strings.Contains(out, "°C</text>") {
		t.Fatalf("N/A ring must have no handles, fill or unit: %s", out)
	}
}

func TestHoldControlRendered(t *testing.T) {
	sched := ui.ScheduleView{Present: true, Active: true, Heading: "Winter season"}
	for _, c := range []struct {
		name string
		hold ui.HoldView
		want string
		not  bool
	}{
		{"permanent pressed", ui.HoldView{Show: true, Mode: "permanent", CanNext: true}, `data-value="permanent" aria-pressed="true"`, false},
		{"none: next not pressed", ui.HoldView{Show: true, Mode: "none", CanNext: true}, `data-value="next" aria-pressed="true"`, true},
		{"none: hold starts next", ui.HoldView{Show: true, Mode: "none", CanNext: true}, `mode.value = &#39;next&#39;`, false},
		{"on: hold ends", ui.HoldView{Show: true, Mode: "next", CanNext: true}, `mode.value = &#39;none&#39;`, false},
		{"no next event: hold opens options", ui.HoldView{Show: true, Mode: "none"}, `mode.value = &#39;next&#39;`, true},
		{"until", ui.HoldView{Show: true, Mode: "until", Time: "22:30"}, `value="22:30"`, false},
		{"hidden", ui.HoldView{}, `hold-ctl`, true},
		{"away options closed until Away is clicked", ui.HoldView{Show: true, Mode: "none"}, `class="away-for" style="display:none"`, false},
		{"away options default to 8 h", ui.HoldView{Show: true, Mode: "none"}, `value="8h" checked`, false},
		{"away click opens the options, sends nothing", ui.HoldView{Show: true, Mode: "none"}, `data-value="away" title="Away: run energy-saving settings while you are out" aria-pressed="false" data-attr:aria-pressed="($away_`, false},
		{"away on: Away ends it", ui.HoldView{Show: true, Mode: "away"}, `mode.value = &#39;none&#39;`, false},
	} {
		var sb strings.Builder
		card := ui.CardView{Slug: "office", Schedule: sched, Hold: c.hold, Control: ui.CardControl{Writable: c.hold.Show}}
		if err := Card(card).Render(context.Background(), &sb); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		if strings.Contains(out, c.want) == c.not {
			t.Errorf("%s: %q in %s", c.name, c.want, out)
		}
	}
}

// The Until button must be morphed with the rest, or its pressed state goes
// stale between polls; only the time input is left alone while it is edited.
func TestHoldUntilButtonIsMorphed(t *testing.T) {
	var sb strings.Builder
	card := ui.CardView{Slug: "office", Schedule: ui.ScheduleView{Present: true, Active: true}, Hold: ui.HoldView{Show: true, Mode: "next", CanNext: true}, Control: ui.CardControl{Writable: true}}
	if err := Card(card).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	i := strings.Index(out, `class="hold-at"`)
	j := strings.Index(out, `data-value="until"`)
	if i < 0 || j < i || strings.Contains(out[i:j], "data-ignore-morph") {
		t.Errorf("Until button is inside an ignore-morph element: %s", out)
	}
	if !strings.Contains(out, `type="time" name="at" value="" data-ignore-morph`) {
		t.Errorf("time input should be ignore-morph: %s", out)
	}
}

// Issue 18: the middle of the fan and vane wheels is the Auto button and says
// so whatever is pressed; the lit wedge shows the value. A wheel with no Auto
// falls back to the pressed choice.
func TestDialCenterIsAuto(t *testing.T) {
	center := func(card ui.CardView) string {
		var sb strings.Builder
		if err := Card(card).Render(context.Background(), &sb); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}
	fans := []ui.Choice{{Value: "auto", Label: "Auto"}, {Value: "low", Label: "Low"}, {Value: "powerful", Label: "Powerful", Pressed: true}}
	vanes := []ui.Choice{{Value: "auto", Label: "Auto"}, {Value: "swing", Label: "Swing", Pressed: true}, {Value: "v1", Label: "1"}}
	out := center(ui.CardView{Slug: "office", Fans: fans, Vanes: vanes})
	if n := strings.Count(out, `dominant-baseline="central">Auto</text>`); n != 2 {
		t.Errorf("want Auto in the middle of both wheels, got %d: %s", n, out)
	}
	if strings.Contains(out, `dominant-baseline="central">Powerful</text>`) || strings.Contains(out, `dominant-baseline="central">Swing</text>`) {
		t.Errorf("middle shows the pressed value: %s", out)
	}
	noAuto := center(ui.CardView{Slug: "house", Fans: []ui.Choice{{Value: "low", Label: "Low", Pressed: true}, {Value: "high", Label: "High"}}})
	if !strings.Contains(noAuto, `dominant-baseline="central">Low</text>`) {
		t.Errorf("no Auto: middle should show the pressed choice: %s", noAuto)
	}
}

// The Min/Max setpoints form appears only when the view says so, with the
// profile's bounds on its inputs and the current limits as values.
func TestLimitsControlRenders(t *testing.T) {
	render := func(l ui.LimitsView) string {
		var sb strings.Builder
		card := ui.CardView{Slug: "house", Limits: l, Control: ui.CardControl{Writable: true, Limits: l.Show}}
		if err := Card(card).Render(context.Background(), &sb); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}
	if out := render(ui.LimitsView{}); strings.Contains(out, "Config") {
		t.Error("shown when not enabled")
	}
	out := render(ui.LimitsView{Show: true, Enabled: true, MinCool: "19", MaxHeat: "24", CoolLo: "19", CoolHi: "30", HeatLo: "10", HeatHi: "28"})
	for _, want := range []string{"Config", `type="range" name="min" value="19" min="19" max="30" step="0.5"`, `type="range" name="max" value="24" min="10" max="28"`, "/ui/units/house/limits", "change__debounce.800ms", `name="enabled" checked`, ">Limits</h4>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestLimitSlidersDisabledUntilEnabled(t *testing.T) {
	var sb strings.Builder
	l := ui.LimitsView{Show: true, MinCool: "19", MaxHeat: "24", CoolLo: "19", CoolHi: "30", HeatLo: "10", HeatHi: "28"}
	card := ui.CardView{Slug: "house", Limits: l, Control: ui.CardControl{Writable: true, Limits: true}}
	if err := Card(card).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sb.String(), `type="range"`); n != 2 || strings.Count(sb.String(), " disabled") != 2 {
		t.Errorf("want two disabled sliders: %s", sb.String())
	}
}

func TestPendingControlSaysPending(t *testing.T) {
	if got := pendingTitle("Heat", true); got != "Heat (pending: waiting for the unit)" {
		t.Fatal(got)
	}
	if got := pendingTitle("Heat", false); got != "Heat" {
		t.Fatal(got)
	}
}
