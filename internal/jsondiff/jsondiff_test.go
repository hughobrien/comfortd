// SPDX-License-Identifier: GPL-3.0-or-later

package jsondiff

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"
)

func TestFlattenNested(t *testing.T) {
	got, err := Flatten(json.RawMessage(`{"a":{"b":1},"c":[{"d":"x"}],"e":{},"f":null}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a.b": "1", "c[0].d": `"x"`, "e": "{}", "f": "null"}
	if !maps.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFlattenNumbersKeepForm(t *testing.T) {
	for in, want := range map[string]string{`{"t":20}`: "20", `{"t":20.5}`: "20.5"} {
		got, err := Flatten(json.RawMessage(in))
		if err != nil {
			t.Fatal(err)
		}
		if got["t"] != want {
			t.Errorf("%s: got %q want %q", in, got["t"], want)
		}
	}
}

func TestFlattenTopLevelArrayAndEmpty(t *testing.T) {
	got, err := Flatten(json.RawMessage(`[{"x":1},[]]`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"[0].x": "1", "[1]": "[]"}
	if !maps.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFlattenInvalid(t *testing.T) {
	if _, err := Flatten(json.RawMessage(`{`)); err == nil {
		t.Fatal("want error")
	}
}

func TestDiffKeyRemoved(t *testing.T) {
	got := Diff(map[string]string{"realValues.spHeat": "20"}, map[string]string{"realValues": "{}"})
	want := []Change{{"realValues", "null", "{}"}, {"realValues.spHeat", "20", "null"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDiffNoChange(t *testing.T) {
	m := map[string]string{"a": "1"}
	if got := Diff(m, maps.Clone(m)); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
