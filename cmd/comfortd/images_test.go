// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

// png is the 8-byte PNG signature, enough for content sniffing.
var png = []byte("\x89PNG\r\n\x1a\n0000")

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestImageCachedOnDisk(t *testing.T) {
	var hits atomic.Int32
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{StatusCode: 200, Body: nopCloser{strings.NewReader(string(png))}, Header: http.Header{}, Request: r}, nil
	})}
	dir := filepath.Join(t.TempDir(), "images")
	c := newImageCache(dir, client)
	for range 3 {
		b, err := c.get("https://cdn.example/x.png")
		if err != nil || string(b) != string(png) {
			t.Fatalf("%q %v", b, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("fetched %d times, want 1", hits.Load())
	}
	// A new cache on the same dir (a restart) must not fetch again.
	if _, err := newImageCache(dir, client).get("https://cdn.example/x.png"); err != nil || hits.Load() != 1 {
		t.Fatalf("restart refetched: %v hits=%d", err, hits.Load())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("dir has %d entries", len(ents))
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestImageRejects(t *testing.T) {
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: nopCloser{strings.NewReader("<html>")}, Header: http.Header{}, Request: r}, nil
	})}
	c := newImageCache(t.TempDir(), client)
	if _, err := c.get("http://cdn.example/x.png"); err == nil {
		t.Error("plain http accepted")
	}
	if _, err := c.get("https://cdn.example/x.png"); err == nil {
		t.Error("non-image accepted")
	}
}

func TestImageHandler(t *testing.T) {
	st := store.New([]store.Unit{{Slug: "office", Name: "Office", Serial: kumotest.OfficeSerial}})
	if err := st.Put("office", "device", []byte(`{"deviceSerial":"X","model":{"gallery":{"imageUrl":"https://cdn.example/x.png"}}}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: nopCloser{strings.NewReader(string(png))}, Header: http.Header{}, Request: r}, nil
	})}
	mux := newMux(deps{Store: st, Images: newImageCache(t.TempDir(), client), Now: time.Now})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/img/office", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() != len(png) {
		t.Fatalf("%d %q %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/img/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown unit: %d", rec.Code)
	}
}
