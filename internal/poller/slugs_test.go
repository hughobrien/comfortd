// SPDX-License-Identifier: GPL-3.0-or-later

package poller

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hughobrien/comfortd/internal/store"
)

func slugsOf(units []store.Unit) []string {
	var out []string
	for _, u := range units {
		out = append(out, u.Slug)
	}
	return out
}

func TestUniqueSlugs(t *testing.T) {
	mk := func(pairs ...string) []store.Unit {
		var us []store.Unit
		for i := 0; i < len(pairs); i += 2 {
			us = append(us, store.Unit{Name: pairs[i], Slug: store.Slug(pairs[i]), Serial: pairs[i+1]})
		}
		return us
	}
	tests := []struct {
		name  string
		units []store.Unit
		want  []string
	}{
		{"distinct untouched", mk("Office", "s2", "House", "s1"), []string{"office", "house"}},
		{"collision, lowest serial keeps base", mk("Office", "s2", "office!", "s1"), []string{"office-2", "office"}},
		{"collision independent of list order", mk("office!", "s1", "Office", "s2"), []string{"office", "office-2"}},
		{"three-way", mk("A b", "s3", "a-b", "s1", "A_B", "s2"), []string{"a-b-3", "a-b", "a-b-2"}},
		{"empty slug", mk("***", "s1"), []string{"unit"}},
		{"two empty", mk("寝室", "s2", "!!", "s1"), []string{"unit-2", "unit"}},
		{"suffix avoids existing slug", mk("Den", "s1", "den", "s2", "Den 2", "s3"), []string{"den", "den-3", "den-2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uniqueSlugs(tc.units)
			if got := slugsOf(tc.units); !slices.Equal(got, tc.want) {
				t.Errorf("slugs = %v, want %v", got, tc.want)
			}
		})
	}
}

// stubAPI serves one site with the given zones, in order.
type stubAPI struct{ zones []string }

func (s stubAPI) Get(_ context.Context, path string) (json.RawMessage, error) {
	if path == "/sites/" {
		return json.RawMessage(`[{"id":"site1","name":"Home"}]`), nil
	}
	return json.RawMessage(`[` + joinComma(s.zones) + `]`), nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

func zoneJSON(id, name, serial string) string {
	return `{"id":"` + id + `","name":` + string(must(json.Marshal(name))) + `,"adapter":{"deviceSerial":"` + serial + `"}}`
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestDiscoverKeepsCollidingUnits(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "discovery.json")
	order1 := stubAPI{zones: []string{zoneJSON("z1", "Office", "ser-b"), zoneJSON("z2", "office!", "ser-a"), zoneJSON("z3", "House", "ser-c"), zoneJSON("z4", "***", "ser-d")}}
	units, err := Discover(context.Background(), order1, cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 4 {
		t.Fatalf("got %d units, want 4: %v", len(units), slugsOf(units))
	}
	bySerial := map[string]string{}
	for _, u := range units {
		bySerial[u.Serial] = u.Slug
	}
	want := map[string]string{"ser-a": "office", "ser-b": "office-2", "ser-c": "house", "ser-d": "unit"}
	for k, v := range want {
		if bySerial[k] != v {
			t.Errorf("serial %s slug = %q, want %q", k, bySerial[k], v)
		}
	}
	// Same zones listed in another order give the same slugs.
	order2 := stubAPI{zones: []string{order1.zones[3], order1.zones[2], order1.zones[1], order1.zones[0]}}
	units2, err := Discover(context.Background(), order2, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units2 {
		if bySerial[u.Serial] != u.Slug {
			t.Errorf("reordered: serial %s slug = %q, want %q", u.Serial, u.Slug, bySerial[u.Serial])
		}
	}
	// store.New keeps all of them.
	if n := len(store.New(units).Units()); n != 4 {
		t.Errorf("store holds %d units, want 4", n)
	}
}
