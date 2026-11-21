// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"slices"

	"github.com/hughobrien/comfortd/internal/store"
)

// pickHomeKitUnits returns the discovered units named in slugs, in order, and
// the slugs that match none. An unknown slug (typically a zone renamed in the
// Comfort app) must not stop the daemon: the web UI and metrics still work.
func pickHomeKitUnits(units []store.Unit, slugs []string) (sel []store.Unit, unknown []string) {
	for _, slug := range slugs {
		i := slices.IndexFunc(units, func(u store.Unit) bool { return u.Slug == slug })
		if i < 0 {
			unknown = append(unknown, slug)
			continue
		}
		sel = append(sel, units[i])
	}
	return sel, unknown
}

func slugList(units []store.Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.Slug)
	}
	return out
}
