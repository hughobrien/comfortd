// SPDX-License-Identifier: GPL-3.0-or-later

// Package jsondiff flattens JSON documents to path/leaf maps and diffs them.
package jsondiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Change is one differing path. Old/New are "null" when the path is absent.
type Change struct{ Path, Old, New string }

// Flatten maps each leaf path to the compact JSON of its value. Empty objects
// and arrays are leaves.
func Flatten(raw json.RawMessage) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := walk(out, "", v); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(out map[string]string, path string, v any) error {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			out[path] = "{}"
			return nil
		}
		for k, c := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if err := walk(out, p, c); err != nil {
				return err
			}
		}
	case []any:
		if len(x) == 0 {
			out[path] = "[]"
			return nil
		}
		for i, c := range x {
			if err := walk(out, path+"["+strconv.Itoa(i)+"]", c); err != nil {
				return err
			}
		}
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Errorf("jsondiff: %w", err)
		}
		out[path] = string(b)
	}
	return nil
}

// Diff returns the changes from old to new, sorted by path.
func Diff(old, new map[string]string) []Change {
	var out []Change
	for p, o := range old {
		n, ok := new[p]
		if !ok {
			n = "null"
		}
		if !ok || n != o {
			out = append(out, Change{p, o, n})
		}
	}
	for p, n := range new {
		if _, ok := old[p]; !ok {
			out = append(out, Change{p, "null", n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
