// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestAssignAIDsFresh(t *testing.T) {
	p := filepath.Join(t.TempDir(), "aids.json")
	got, err := AssignAIDs(p, []string{"S1"})
	if err != nil || !maps.Equal(got, map[string]uint64{"S1": 2}) {
		t.Fatalf("got %v err %v", got, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestAssignAIDsStable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "aids.json")
	AssignAIDs(p, []string{"S1"})
	got, _ := AssignAIDs(p, []string{"S2", "S1"})
	if !maps.Equal(got, map[string]uint64{"S1": 2, "S2": 3}) {
		t.Fatalf("got %v", got)
	}
	got, _ = AssignAIDs(p, []string{"S2"})
	if got["S2"] != 3 {
		t.Fatalf("got %v", got)
	}
	got, _ = AssignAIDs(p, []string{"S1", "S2", "S3"})
	if got["S1"] != 2 || got["S3"] != 4 {
		t.Fatalf("S1 not kept / S3 reused an id: %v", got)
	}
}
