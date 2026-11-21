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

func TestImageRedirectLeavingHTTPSRefused(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		w.Write(png)
	}))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/x.png", http.StatusFound)
	}))
	defer tls.Close()
	c := newImageCache(t.TempDir(), tls.Client())
	if _, err := c.get(tls.URL + "/x.png"); err == nil {
		t.Fatal("redirect to http followed")
	}
	if plainHits.Load() != 0 {
		t.Fatalf("plain http server was contacted %d times", plainHits.Load())
	}
}

func TestImageFailureNegativeCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	now := time.Now()
	c := newImageCache(t.TempDir(), srv.Client())
	c.now = func() time.Time { return now }
	for range 3 {
		if _, err := c.get(srv.URL + "/x.png"); err == nil {
			t.Fatal("failure not reported")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hit %d times within the window, want 1", hits.Load())
	}
	now = now.Add(failTTL + time.Second)
	c.get(srv.URL + "/x.png")
	if hits.Load() != 2 {
		t.Fatalf("upstream hit %d times after the window, want 2", hits.Load())
	}
}

func TestImageSlowFetchDoesNotBlockOtherUnits(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.png" {
			close(started)
			<-release
		}
		w.Write(png)
	}))
	defer srv.Close()
	c := newImageCache(t.TempDir(), srv.Client())
	slowDone := make(chan struct{})
	go func() { c.get(srv.URL + "/slow.png"); close(slowDone) }()
	// Let the slow fetch finish before TempDir cleanup removes its target.
	defer func() { close(release); <-slowDone }()
	<-started
	done := make(chan error, 1)
	go func() { _, err := c.get(srv.URL + "/fast.png"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fast fetch blocked behind slow one")
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
