// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

// holdTap answers hold requests itself (recording them) and passes the rest to the fake.
type holdTap struct {
	status int
	reqs   []map[string]any
	paths  []string
}

func (h *holdTap) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(r.URL.Path, "/hold") {
		return http.DefaultTransport.RoundTrip(r)
	}
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	h.reqs, h.paths = append(h.reqs, m), append(h.paths, r.Method+" "+r.URL.Path)
	return &http.Response{StatusCode: h.status, Body: io.NopCloser(bytes.NewReader([]byte(`{}`))), Header: http.Header{}}, nil
}

func holdClient(t *testing.T, status int) (*Client, *holdTap) {
	t.Helper()
	e := newEnv(t, kumotest.Password)
	tap := &holdTap{status: status}
	return New(Options{BaseURL: e.srv.URL, CredentialsPath: e.creds, TokenPath: filepath.Join(e.dir, "t.json"),
		HTTP: &http.Client{Transport: tap}, Sleep: func(context.Context, time.Duration) error { return nil }}), tap
}

func zoneOf(m map[string]any) map[string]any { return m["zones"].([]any)[0].(map[string]any) }

func TestSetHoldBody(t *testing.T) {
	c, tap := holdClient(t, 201)
	until := time.Date(2026, 11, 17, 6, 22, 0, 0, time.UTC)
	if err := c.SetHold(context.Background(), "S", "Z", &until); err != nil {
		t.Fatal(err)
	}
	if err := c.SetHold(context.Background(), "S", "Z", nil); err != nil {
		t.Fatal(err)
	}
	if tap.paths[0] != "POST /v4/sites/S/hold" {
		t.Fatalf("path %v", tap.paths)
	}
	m := tap.reqs[0]
	z := zoneOf(m)
	if m["siteId"] != "S" || m["endTime"] != float64(1794896520) || z["id"] != "Z" || z["enabled"] != true || z["type"] != "hold" {
		t.Fatalf("timed body %v", m)
	}
	if v, ok := tap.reqs[1]["endTime"]; !ok || v != nil {
		t.Fatalf("permanent body must carry endTime null: %v", tap.reqs[1])
	}
}

func TestEndHoldBody(t *testing.T) {
	c, tap := holdClient(t, 201)
	if err := c.EndHold(context.Background(), "S", "Z"); err != nil {
		t.Fatal(err)
	}
	m := tap.reqs[0]
	if _, has := m["endTime"]; has || zoneOf(m)["enabled"] != false || zoneOf(m)["id"] != "Z" || m["siteId"] != "S" {
		t.Fatalf("end body %v", m)
	}
}

func TestHoldErrorIsHTTPError(t *testing.T) {
	c, _ := holdClient(t, 400)
	var he *HTTPError
	if err := c.EndHold(context.Background(), "S", "Z"); !errors.As(err, &he) || he.Status != 400 {
		t.Fatalf("err %v", err)
	}
}

func TestParseZonesHoldType(t *testing.T) {
	zs, err := ParseZones([]byte(`[{"id":"z","name":"A","holdMode":{"enabled":true,"holdType":"permanent","endTime":"2126-11-17T02:56:41.000Z"}}]`))
	if err != nil || zs[0].HoldType != "permanent" || !zs[0].HoldEnabled {
		t.Fatalf("%+v %v", zs, err)
	}
}
