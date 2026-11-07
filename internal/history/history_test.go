// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func rec(unit string, i int) Record {
	return Record{T: t0.Add(time.Duration(i) * time.Second), Unit: unit, Endpoint: "device", Path: "spHeat", Old: "19", New: "21"}
}

func open(t *testing.T, max int64) (*Log, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history.jsonl")
	l, err := Open(p, max)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, p
}

func TestAppendAndRecent(t *testing.T) {
	l, _ := open(t, DefaultMaxBytes)
	for i := range 3 {
		if err := l.Append(rec("office", i)); err != nil {
			t.Fatal(err)
		}
	}
	l.Append(rec("house", 10))
	got := l.Recent("office", 10)
	if len(got) != 3 || !got[0].T.After(got[2].T) {
		t.Fatalf("office: %v", got)
	}
	if got := l.Recent("", 2); len(got) != 2 || got[0].Unit != "house" {
		t.Fatalf("all: %v", got)
	}
}

func TestRecentSurvivesReopen(t *testing.T) {
	l, p := open(t, DefaultMaxBytes)
	l.Append(rec("office", 0), rec("office", 1))
	want := l.Recent("", 10)
	l.Close()
	l2, err := Open(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if got := l2.Recent("", 10); !slices.EqualFunc(got, want, func(a, b Record) bool { return a == b }) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRotate(t *testing.T) {
	l, p := open(t, 200)
	for i := range 6 {
		l.Append(rec("office", i))
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Size() >= 200 {
		t.Fatalf("main file %d bytes", st.Size())
	}
	if n := len(l.Recent("office", 100)); n != 6 {
		t.Fatalf("recent has %d", n)
	}
}

func TestRecordShape(t *testing.T) {
	l, p := open(t, DefaultMaxBytes)
	l.Append(rec("office", 0))
	f, _ := os.Open(p)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	var m map[string]any
	if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 6 {
		t.Fatalf("keys: %v", m)
	}
	for _, k := range []string{"t", "unit", "endpoint", "path", "old", "new"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestChangedSince(t *testing.T) {
	l, _ := open(t, DefaultMaxBytes)
	l.Append(rec("office", 0))
	if !l.ChangedSince("office", "device", t0.Add(-time.Minute))["spHeat"] {
		t.Error("want changed")
	}
	if l.ChangedSince("office", "device", t0.Add(time.Minute))["spHeat"] {
		t.Error("want unchanged")
	}
}

func TestSubscribe(t *testing.T) {
	l, _ := open(t, DefaultMaxBytes)
	var n int
	l.Subscribe(func(Record) { n++ })
	l.Append(rec("office", 0), rec("office", 1))
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
}

func TestRingCapped(t *testing.T) {
	l, _ := open(t, DefaultMaxBytes)
	for i := range 1100 {
		l.Append(rec("office", i))
	}
	if n := len(l.Recent("", 5000)); n != 1000 {
		t.Fatalf("n=%d", n)
	}
}
