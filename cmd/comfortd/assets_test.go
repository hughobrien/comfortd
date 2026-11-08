// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

func TestIndexServesPage(t *testing.T) {
	_, d, _ := polledDeps(t)
	h := newMux(d)
	rec := get(h, "/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{`data-init="@get(&#39;/ui/sse&#39;, {retry`, "/ui/static/datastar.js", "/ui/static/favicon.svg", `id="units"`, `id="feed"`, `id="unit-office"`} {
		if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, "&#39;", "'")) {
			t.Errorf("body missing %s", want)
		}
	}
	js := get(h, "/ui/static/datastar.js")
	if js.Code != 200 || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("js: %d %s", js.Code, js.Header().Get("Content-Type"))
	}
	if css := get(h, "/ui/static/style.css"); css.Code != 200 || !strings.Contains(css.Header().Get("Content-Type"), "css") {
		t.Fatalf("css: %d", css.Code)
	}
	if ico := get(h, "/ui/static/favicon.svg"); ico.Code != 200 || ico.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("favicon: %d %s", ico.Code, ico.Header().Get("Content-Type"))
	}
	if get(h, "/nope").Code != 404 {
		t.Error("unknown path should 404")
	}
}
