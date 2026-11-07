// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// AssignAIDs loads the serial to accessory ID map at path, gives each missing
// serial the next ID (starting at 2) and saves it atomically with mode 0600.
// IDs are never reused or renumbered; serials not in the list are kept.
func AssignAIDs(path string, serials []string) (map[string]uint64, error) {
	m := map[string]uint64{}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	next := uint64(2)
	for _, id := range m {
		if id >= next {
			next = id + 1
		}
	}
	changed := false
	for _, s := range serials {
		if _, ok := m[s]; !ok {
			m[s] = next
			next++
			changed = true
		}
	}
	if changed {
		if err := writeAtomic(path, m); err != nil {
			return nil, err
		}
	}
	out := map[string]uint64{}
	for _, s := range serials {
		out[s] = m[s]
	}
	return out, nil
}

func writeAtomic(path string, m map[string]uint64) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aids-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
