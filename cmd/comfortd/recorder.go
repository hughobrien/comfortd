// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/hughobrien/comfortd/internal/history"
	"github.com/hughobrien/comfortd/internal/jsondiff"
	"github.com/hughobrien/comfortd/internal/store"
)

// recordUpdate appends one history record per changed leaf. First sight of an
// endpoint (Old nil) records nothing.
func recordUpdate(h *history.Log, u store.Update) error {
	if u.Old == nil {
		return nil
	}
	oldF, err := jsondiff.Flatten(u.Old)
	if err != nil {
		return err
	}
	newF, err := jsondiff.Flatten(u.New)
	if err != nil {
		return err
	}
	var recs []history.Record
	for _, c := range jsondiff.Diff(oldF, newF) {
		recs = append(recs, history.Record{T: u.At.UTC(), Unit: u.Slug, Endpoint: u.Endpoint, Path: c.Path, Old: c.Old, New: c.New})
	}
	if len(recs) == 0 {
		return nil
	}
	return h.Append(recs...)
}
