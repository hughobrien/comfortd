// SPDX-License-Identifier: GPL-3.0-or-later

// Package fsutil holds small filesystem helpers.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a temp file and rename, with mode 0600.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() { os.Remove(tmp) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
