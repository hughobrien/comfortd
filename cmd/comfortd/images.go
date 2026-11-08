// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
)

const maxImageBytes = 4 << 20

// imageCache fetches each unit's model picture once, server side, and keeps
// it on disk, so browsers never contact the CDN (and never tell it their IP).
type imageCache struct {
	dir    string
	client *http.Client
	mu     sync.Mutex
}

func newImageCache(dir string, client *http.Client) *imageCache {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &imageCache{dir: dir, client: client}
}

// get returns the cached bytes for url, fetching and storing them first if needed.
func (c *imageCache) get(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("image url is not https")
	}
	path := filepath.Join(c.dir, ui.ImageKey(url))
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxImageBytes {
		return nil, fmt.Errorf("image larger than %d bytes", maxImageBytes)
	}
	if !strings.HasPrefix(http.DetectContentType(b), "image/") {
		return nil, fmt.Errorf("not an image")
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(c.dir, ".img-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, err
	}
	return b, nil
}

// handleImage serves GET /ui/img/{slug}: the unit's model picture from the cache.
func (d deps) handleImage(w http.ResponseWriter, r *http.Request) {
	sn, ok := d.Store.Get(r.PathValue("slug"))
	if !ok || d.Images == nil || sn.Device == nil || sn.Device.ImageURL == "" {
		http.NotFound(w, r)
		return
	}
	b, err := d.Images.get(sn.Device.ImageURL)
	if err != nil {
		slog.Warn("model image", "unit", sn.Unit.Slug, "err", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	// The page URL carries ?v=<key>, so a changed picture is a new URL.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}
