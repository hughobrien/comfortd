// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func startDaemon(t *testing.T, extra ...string) (base string, ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	done := make(chan int, 1)
	args := append([]string{"-fake", "-listen", "127.0.0.1:0", "-state-dir", t.TempDir()}, extra...)
	go func() { done <- runContext(ctx, args, io.Discard, func(a net.Addr) { addrCh <- a }) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case a := <-addrCh:
		return "http://" + a.String(), ctx
	case code := <-done:
		t.Fatalf("exited %d", code)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	return "", ctx
}

func TestHomekitPage(t *testing.T) {
	base, _ := startDaemon(t, "-homekit-units", "office", "-hap-port", "0")
	resp, err := http.Get(base + "/homekit")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !regexp.MustCompile(`\d{3}-\d{2}-\d{3}`).Match(b) || !regexp.MustCompile(`Office`).Match(b) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestHomekitPageOffIs404(t *testing.T) {
	base, _ := startDaemon(t)
	resp, err := http.Get(base + "/homekit")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
}

func TestUnknownHomekitUnit(t *testing.T) {
	var out io.Writer = io.Discard
	if code := run([]string{"-fake", "-listen", "127.0.0.1:0", "-state-dir", t.TempDir(), "-homekit-units", "nope"}, out); code == 0 {
		t.Fatal("want non-zero exit")
	}
}

func TestInactiveUnitIsPlaceholder(t *testing.T) {
	base, _ := startDaemon(t, "-inactive-units", "house")
	get := func(p string) string {
		resp, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if page := get("/"); !strings.Contains(page, "not enabled yet") || strings.Contains(page, "TESTSERIAL0002") {
		t.Fatalf("page: %s", page)
	}
	if !strings.Contains(get("/v1/units"), "house") {
		t.Fatal("API should still list house")
	}
}
