// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sync"

	"github.com/hughobrien/comfortd/internal/fsutil"
)

// orderStore remembers the order the user dragged the unit cards into. A nil
// store keeps the default order.
type orderStore struct {
	path  string
	mu    sync.Mutex
	slugs []string
}

func loadOrder(path string) *orderStore {
	o := &orderStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &o.slugs); err != nil {
			slog.Warn("order: ignoring unreadable file", "path", path, "err", err)
		}
	}
	return o
}

// rank is a unit's place in the saved order; unknown units sort after the rest.
func (o *orderStore) rank(slug string) int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if i := slices.Index(o.slugs, slug); i >= 0 {
		return i
	}
	return len(o.slugs)
}

// set saves the order, keeping only slugs in known and each once.
func (o *orderStore) set(slugs, known []string) error {
	var clean []string
	for _, s := range slugs {
		if slices.Contains(known, s) && !slices.Contains(clean, s) {
			clean = append(clean, s)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	b, _ := json.Marshal(clean)
	if err := fsutil.WriteFileAtomic(o.path, b); err != nil {
		return err
	}
	o.slugs = clean
	return nil
}

func (d deps) handleUIOrder(w http.ResponseWriter, r *http.Request) {
	if !uiOK(w, r) {
		return
	}
	if d.Order == nil {
		http.Error(w, "ordering is off", http.StatusNotFound)
		return
	}
	var in struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var known []string
	for _, u := range d.Store.Units() {
		known = append(known, u.Slug)
	}
	if err := d.Order.set(in.Order, known); err != nil {
		slog.Warn("order: save failed", "err", err)
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
