// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrderPersistsAndFiltersUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "order.json")
	o := loadOrder(path)
	if err := o.set([]string{"office", "ghost", "house", "office"}, []string{"house", "office"}); err != nil {
		t.Fatal(err)
	}
	o = loadOrder(path)
	if o.rank("office") != 0 || o.rank("house") != 1 || o.rank("new") != 2 {
		t.Fatalf("ranks %d %d %d", o.rank("office"), o.rank("house"), o.rank("new"))
	}
	var none *orderStore
	if none.rank("office") != 0 {
		t.Fatal("nil store should keep default order")
	}
}

func TestUIOrderEndpoint(t *testing.T) {
	_, d := ctlDeps(t)
	d.Order = loadOrder(filepath.Join(t.TempDir(), "order.json"))
	mux := newMux(d)
	post := func(hdr bool) int {
		r := httptest.NewRequest("POST", "/ui/order", strings.NewReader(`{"order":["office","house"]}`))
		if hdr {
			r.Header.Set("Datastar-Request", "true")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if c := post(false); c != http.StatusForbidden {
		t.Fatalf("no header: %d", c)
	}
	if c := post(true); c != http.StatusNoContent {
		t.Fatalf("got %d", c)
	}
	views := d.unitViews()
	if views[0].Slug != "office" {
		t.Fatalf("first card %s", views[0].Slug)
	}
}
