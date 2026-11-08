// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/cmd/comfortd/ui"
	"github.com/hughobrien/comfortd/cmd/comfortd/ui/templates"
	"github.com/hughobrien/comfortd/internal/control"
	"github.com/hughobrien/comfortd/internal/poller"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

// ctlDeps returns deps with a running controller (office writable) against the fake.
func ctlDeps(t *testing.T) (*kumotest.Server, deps) {
	t.Helper()
	srv := kumotest.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	creds := filepath.Join(dir, "c")
	os.WriteFile(creds, []byte(kumotest.User+"\n"+kumotest.Password+"\n"), 0o600)
	c := kumo.New(kumo.Options{BaseURL: srv.URL, CredentialsPath: creds})
	units, err := poller.Discover(context.Background(), c, filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(units)
	p := poller.New(c, st, time.Minute)
	p.PollSlow(context.Background())
	p.PollOnce(context.Background())
	ctl := control.New(control.Options{Sender: c, Store: st, Writable: []string{"office"}, Window: 20 * time.Millisecond})
	st.Subscribe(ctl.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ctl.Run(ctx)
	now := time.Now()
	return srv, deps{Store: st, Ctl: ctl, Interval: time.Minute, Now: func() time.Time { return now }}
}

func post(h http.Handler, path, ctype, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCommandEndpointQueues(t *testing.T) {
	srv, d := ctlDeps(t)
	rec := post(newMux(d), "/v1/units/office/command", "application/json", `{"spHeat":21}`, nil)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.Commands()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cmds := srv.Commands(); len(cmds) != 1 || cmds[0].Commands["spHeat"] != 21.0 {
		t.Fatalf("got %+v", cmds)
	}
}

func TestCommandEndpointForbidden(t *testing.T) {
	srv, d := ctlDeps(t)
	if rec := post(newMux(d), "/v1/units/house/command", "application/json", `{"spHeat":21}`, nil); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec := post(newMux(d), "/v1/units/nope/command", "application/json", `{"spHeat":21}`, nil); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if len(srv.Commands()) != 0 {
		t.Fatal("command sent")
	}
}

func TestCommandEndpointInvalid(t *testing.T) {
	_, d := ctlDeps(t)
	if rec := post(newMux(d), "/v1/units/office/command", "application/json", `{"spHeat":99}`, nil); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := post(newMux(d), "/v1/units/office/command", "application/json", `{"bogus":1}`, nil); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestCommandEndpointNeedsJSON(t *testing.T) {
	_, d := ctlDeps(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		if rec := post(newMux(d), "/v1/units/office/command", ct, `{"spHeat":21}`, nil); rec.Code != 415 {
			t.Fatalf("%q: %d", ct, rec.Code)
		}
	}
}

func TestUIPostNeedsDatastarHeader(t *testing.T) {
	srv, d := ctlDeps(t)
	h := newMux(d)
	if rec := post(h, "/ui/units/office/mode?m=vent", "", "", nil); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec := post(h, "/ui/units/office/spheat?delta=0.5", "", "", nil); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if len(srv.Commands()) != 0 {
		t.Fatal("command sent without header")
	}
	if rec := post(h, "/ui/units/office/mode?m=vent", "", "", map[string]string{"Datastar-Request": "true"}); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if rec := post(h, "/ui/units/office/spheat?delta=0.5", "", "", map[string]string{"Datastar-Request": "true"}); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if rec := post(h, "/ui/units/house/mode?m=off", "", "", map[string]string{"Datastar-Request": "true"}); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec := post(h, "/ui/units/office/mode?m=auto", "", "", map[string]string{"Datastar-Request": "true"}); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestCardButtonsOnlyWhenWritable(t *testing.T) {
	_, d := ctlDeps(t)
	h := newMux(d)
	// The index renders nothing server-side for units until SSE; use the view builder.
	now := d.Now()
	var office, house string
	for _, sn := range d.Store.All() {
		v := d.viewFor(sn, now)
		var sb strings.Builder
		if err := renderCard(&sb, v.Card); err != nil {
			t.Fatal(err)
		}
		if sn.Unit.Slug == "office" {
			office = sb.String()
		} else {
			house = sb.String()
		}
	}
	_ = h
	if !strings.Contains(office, "/ui/units/office/mode?m=heat") {
		t.Errorf("office has no buttons: %s", office)
	}
	if strings.Contains(house, "@post") {
		t.Errorf("house has buttons: %s", house)
	}
}

func renderCard(w *strings.Builder, c ui.CardView) error {
	return templates.Card(c).Render(context.Background(), w)
}
