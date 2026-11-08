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
