// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brutella/hap"
)

func TestLoadOrGeneratePinPersistsAndStrong(t *testing.T) {
	dir := t.TempDir()
	p1, err := LoadOrGeneratePin(dir)
	if err != nil || len(p1) != 8 || hap.InvalidPins[p1] {
		t.Fatalf("pin %q err %v", p1, err)
	}
	p2, _ := LoadOrGeneratePin(dir)
	if p1 != p2 {
		t.Fatalf("not persisted: %s != %s", p1, p2)
	}
}

func TestPinFileMode(t *testing.T) {
	dir := t.TempDir()
	LoadOrGeneratePin(dir)
	st, err := os.Stat(filepath.Join(dir, "pin.txt"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}
}

func TestInvalidPinRegenerated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pin.txt"), []byte("11111111\n"), 0o600)
	p, _ := LoadOrGeneratePin(dir)
	if p == "11111111" {
		t.Fatal("weak pin kept")
	}
}

func TestFormatPin(t *testing.T) {
	if got := FormatPin("12345678"); got != "123-45-678" {
		t.Fatalf("got %q", got)
	}
}
