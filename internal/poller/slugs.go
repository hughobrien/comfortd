// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"log/slog"
	"sort"
	"strconv"

	"github.com/hughobrien/comfortd/internal/store"
)

// uniqueSlugs makes every unit's slug non-empty and distinct. The slug is a
// persistent identity (URLs, metric labels, state files), so a unit with a
// unique, non-empty name keeps its slug untouched. Only collisions and
// names with no ASCII letters or digits are changed: they are ordered by
// serial (so the result does not depend on cloud list order) and the first
// keeps the base slug, the rest get -2, -3, ... The serial itself never
// appears in a slug. Each change is logged.
func uniqueSlugs(units []store.Unit) {
	count := map[string]int{}
	for _, u := range units {
		count[u.Slug]++
	}
	used := map[string]bool{}
	var pending []int
	for i, u := range units {
		if u.Slug != "" && count[u.Slug] == 1 {
			used[u.Slug] = true
		} else {
			pending = append(pending, i)
		}
	}
	sort.SliceStable(pending, func(a, b int) bool { return units[pending[a]].Serial < units[pending[b]].Serial })
	for _, i := range pending {
		base := units[i].Slug
		if base == "" {
			base = "unit"
		}
		slug := base
		for n := 2; used[slug]; n++ {
			slug = base + "-" + strconv.Itoa(n)
		}
		used[slug] = true
		slog.Warn("unit slug empty or shared; disambiguated", "zone", units[i].Name, "slug", slug)
		units[i].Slug = slug
	}
}
