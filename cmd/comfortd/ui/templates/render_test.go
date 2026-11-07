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
