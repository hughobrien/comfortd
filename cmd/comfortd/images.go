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

const (
	maxImageBytes = 4 << 20
	maxRedirects  = 3
	// failTTL is how long a failed fetch is remembered, so a down CDN costs
	// one outbound request per picture per window rather than one per page view.
	failTTL = 5 * time.Minute
)

// imageCache fetches each unit's model picture once, server side, and keeps
// it on disk, so browsers never contact the CDN (and never tell it their IP).
type imageCache struct {
	dir    string
	client *http.Client
	now    func() time.Time // injectable for tests

	mu     sync.Mutex // guards the two maps only, never held across I/O
	flying map[string]*flight
	failed map[string]failure
}

// flight is one in-progress fetch that concurrent requests for the same
// picture wait on.
type flight struct {
	done chan struct{}
	b    []byte
	err  error
}

type failure struct {
	err   error
	until time.Time
}

// httpsOnly refuses redirects that leave https (the first URL is checked in get).
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("image redirect to non-https url refused")
	}
	if len(via) > maxRedirects {
		return fmt.Errorf("image fetch: too many redirects")
	}
	return nil
}

func newImageCache(dir string, client *http.Client) *imageCache {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if client.CheckRedirect == nil {
		cp := *client
		cp.CheckRedirect = httpsOnly
		client = &cp
	}
	return &imageCache{dir: dir, client: client, now: time.Now,
		flying: map[string]*flight{}, failed: map[string]failure{}}
}

// get returns the cached bytes for url, fetching and storing them first if needed.
func (c *imageCache) get(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("image url is not https")
	}
	path := filepath.Join(c.dir, ui.ImageKey(url))
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}
	c.mu.Lock()
	if f, ok := c.failed[path]; ok {
		if c.now().Before(f.until) {
			c.mu.Unlock()
			return nil, f.err
		}
		delete(c.failed, path)
	}
	if f, ok := c.flying[path]; ok {
		c.mu.Unlock()
		<-f.done
		return f.b, f.err
	}
	f := &flight{done: make(chan struct{})}
	c.flying[path] = f
	c.mu.Unlock()

	f.b, f.err = c.fetch(url, path)

	c.mu.Lock()
	delete(c.flying, path)
	if f.err != nil {
		c.failed[path] = failure{err: f.err, until: c.now().Add(failTTL)}
	}
	c.mu.Unlock()
	close(f.done)
	return f.b, f.err
}

// fetch downloads url and stores it at path.
func (c *imageCache) fetch(url, path string) ([]byte, error) {
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
