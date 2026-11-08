// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

// readEvent reads one SSE event (up to a blank line) from sc, ignoring comments.
func readEvent(t *testing.T, sc *bufio.Scanner) string {
	t.Helper()
	var b strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if b.Len() > 0 {
				return b.String()
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		b.WriteString(line + "\n")
	}
	t.Fatalf("stream ended: %v (got %q)", sc.Err(), b.String())
	return ""
}

func openSSE(t *testing.T, d deps, hdr map[string]string) (*bufio.Scanner, func()) {
	t.Helper()
	srv := httptest.NewServer(newMux(d))
	req, _ := http.NewRequest("GET", srv.URL+"/ui/sse", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("missing X-Accel-Buffering")
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	return sc, func() { resp.Body.Close(); srv.CloseClientConnections(); srv.Close() }
}

func TestSSEInitialState(t *testing.T) {
	_, d, _ := polledDeps(t)
	d.Hub = newPushHub()
	sc, done := openSSE(t, d, nil)
	defer done()
	var all string
	for range 2 {
		ev := readEvent(t, sc)
		if !strings.Contains(ev, "datastar-patch-elements") {
			t.Fatalf("event: %.200s", ev)
		}
		all += ev
	}
	if !strings.Contains(all, "unit-office") || !strings.Contains(all, "unit-house") {
		t.Fatalf("missing units: %.300s", all)
	}
	if !strings.Contains(all, "mode inner") {
		t.Error("cold load should use inner mode first")
	}
}

func TestSSEReconnectUsesOuter(t *testing.T) {
	_, d, _ := polledDeps(t)
	d.Hub = newPushHub()
	sc, done := openSSE(t, d, map[string]string{"Last-Event-ID": "x"})
	defer done()
	ev := readEvent(t, sc)
	// datastar-go omits the mode line for its default, outer.
	if !strings.Contains(ev, "selector #unit-") || strings.Contains(ev, "mode ") {
		t.Fatalf("event: %.300s", ev)
	}
}

func TestSSEPushesUnitOnStorePut(t *testing.T) {
	srv, d, _ := polledDeps(t)
	d.Hub = newPushHub()
	d.Store.Subscribe(func(u store.Update) { d.Hub.Publish(pushEvent{Slug: u.Slug}) })
	sc, done := openSSE(t, d, nil)
	defer done()
	readEvent(t, sc)
	readEvent(t, sc)
	readEvent(t, sc) // the build check
	srv.SetField("TESTSERIAL0001", "spHeat", 23)
	raw, _ := d.Store.Get("office")
	changed := strings.Replace(string(raw.Raw["device"]), `"spHeat": 19`, `"spHeat": 23`, 1)
	if changed == string(raw.Raw["device"]) {
		changed = strings.Replace(string(raw.Raw["device"]), `"spHeat":19`, `"spHeat":23`, 1)
	}
	if err := d.Store.Put("office", "device", []byte(changed), time.Now()); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() { got <- readEvent(t, sc) }()
	select {
	case ev := <-got:
		if !strings.Contains(ev, "selector #unit-office") || strings.Contains(ev, "mode ") {
			t.Fatalf("event: %.300s", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no push within 2s")
	}
}

func TestSSEChecksBuildID(t *testing.T) {
	old := templates.BuildID
	templates.BuildID = "abc123"
	defer func() { templates.BuildID = old }()
	_, d, _ := polledDeps(t)
	d.Hub = newPushHub()
	sc, done := openSSE(t, d, nil)
	defer done()
	var all string
	for range 3 { // two units, then the check
		all += readEvent(t, sc)
	}
	if !strings.Contains(all, `m.content!=="abc123"`) || !strings.Contains(all, "location.reload()") {
		t.Fatalf("no build check: %.400s", all)
	}
}
