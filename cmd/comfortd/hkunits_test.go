// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"slices"
	"testing"

	"github.com/hughobrien/comfortd/internal/store"
)

func TestPickHomeKitUnits(t *testing.T) {
	units := []store.Unit{{Slug: "office"}, {Slug: "house"}}
	tests := []struct {
		name        string
		slugs       []string
		sel, unknow []string
	}{
		{"all known", []string{"house", "office"}, []string{"house", "office"}, nil},
		{"one unknown carries on", []string{"office", "study"}, []string{"office"}, []string{"study"}},
		{"all unknown", []string{"study"}, nil, []string{"study"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, unknown := pickHomeKitUnits(units, tc.slugs)
			var got []string
			for _, u := range sel {
				got = append(got, u.Slug)
			}
			if !slices.Equal(got, tc.sel) || !slices.Equal(unknown, tc.unknow) {
				t.Errorf("sel=%v unknown=%v, want %v %v", got, unknown, tc.sel, tc.unknow)
			}
		})
	}
}
