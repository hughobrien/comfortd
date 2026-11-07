// SPDX-License-Identifier: GPL-3.0-or-later

package annotate

import (
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := s.Lookup("device", "operationMode")
	if !ok || f.Label != "Mode" {
		t.Fatalf("%+v %v", f, ok)
	}
}

func TestLoadRejectsBadConfidence(t *testing.T) {
	_, err := Parse([]byte("[[field]]\nendpoint='device'\npath='x'\nlabel='X'\nconfidence='sure'\n"))
	if err == nil || !strings.Contains(err.Error(), "x") || !strings.Contains(err.Error(), "sure") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadRejectsDuplicate(t *testing.T) {
	f := "[[field]]\nendpoint='device'\npath='x'\nlabel='X'\nconfidence='assumed'\n"
	if _, err := Parse([]byte(f + f)); err == nil {
		t.Fatal("want error")
	}
}

func TestDecodeUnknownValue(t *testing.T) {
	s, _ := Load()
	f, _ := s.Lookup("device", "operationMode")
	if m, ok := f.Decode(`"cool"`); ok || m != "" {
		t.Fatalf("%q %v", m, ok)
	}
	if m, ok := f.Decode(`"vent"`); !ok || m != "Fan only" {
		t.Fatalf("%q %v", m, ok)
	}
}
