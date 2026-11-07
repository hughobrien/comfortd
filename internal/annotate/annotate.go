// SPDX-License-Identifier: GPL-3.0-or-later

// Package annotate holds what we know about Kumo API fields.
package annotate

import (
	_ "embed"
	"fmt"

	"github.com/BurntSushi/toml"
)

//go:embed annotations.toml
var embedded []byte

type Field struct {
	Endpoint, Path, Label, Confidence, Note string
	Values                                  map[string]string // raw compact JSON value → meaning
}

type Set struct{ fields map[[2]string]Field }

var confidences = map[string]bool{"app-confirmed": true, "api-only": true, "assumed": true, "hypothesis": true}

// Load parses the embedded annotations.
func Load() (*Set, error) { return Parse(embedded) }

func Parse(data []byte) (*Set, error) {
	var doc struct {
		Field []struct {
			Endpoint   string            `toml:"endpoint"`
			Path       string            `toml:"path"`
			Label      string            `toml:"label"`
			Confidence string            `toml:"confidence"`
			Note       string            `toml:"note"`
			Values     map[string]string `toml:"values"`
		} `toml:"field"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("annotate: %w", err)
	}
	s := &Set{fields: map[[2]string]Field{}}
	for _, f := range doc.Field {
		if f.Endpoint == "" || f.Path == "" {
			return nil, fmt.Errorf("annotate: field with empty endpoint or path (%q %q)", f.Endpoint, f.Path)
		}
		if !confidences[f.Confidence] {
			return nil, fmt.Errorf("annotate: %s %s: unknown confidence %q", f.Endpoint, f.Path, f.Confidence)
		}
		k := [2]string{f.Endpoint, f.Path}
		if _, dup := s.fields[k]; dup {
			return nil, fmt.Errorf("annotate: duplicate field %s %s", f.Endpoint, f.Path)
		}
		s.fields[k] = Field{f.Endpoint, f.Path, f.Label, f.Confidence, f.Note, f.Values}
	}
	return s, nil
}

func (s *Set) Lookup(endpoint, path string) (Field, bool) {
	if s == nil {
		return Field{}, false
	}
	f, ok := s.fields[[2]string{endpoint, path}]
	return f, ok
}

// Decode returns the meaning of a raw compact-JSON value.
func (f Field) Decode(rawValue string) (string, bool) {
	m, ok := f.Values[rawValue]
	return m, ok
}
