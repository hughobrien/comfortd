// SPDX-License-Identifier: GPL-3.0-or-later

package kumotest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func do(t *testing.T, method, url, token string, appVersion bool, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if appVersion {
		req.Header.Set("x-app-version", "3.2.4")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func login(t *testing.T, s *Server) (access, refresh string) {
	t.Helper()
	resp, b := do(t, "POST", s.URL+"/login", "", true, map[string]string{"username": User, "password": Password, "appVersion": "3.2.4"})
	if resp.StatusCode != 200 {
		t.Fatalf("login status %d", resp.StatusCode)
	}
	var out struct {
		Token struct{ Access, Refresh string } `json:"token"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Token.Access, out.Token.Refresh
}

func TestLoginIssuesTokens(t *testing.T) {
	s := New()
	defer s.Close()
	access, _ := login(t, s)
	if access == "" {
		t.Fatal("empty access token")
	}
	resp, _ := do(t, "POST", s.URL+"/login", "", true, map[string]string{"username": User, "password": "wrong"})
	if resp.StatusCode != 403 {
		t.Fatalf("wrong password status = %d, want 403", resp.StatusCode)
	}
	if s.Logins() != 2 {
		t.Fatalf("Logins() = %d, want 2", s.Logins())
	}
}

func TestAuthRequired(t *testing.T) {
	s := New()
	defer s.Close()
	access, _ := login(t, s)
	if resp, _ := do(t, "GET", s.URL+"/sites/", "", true, nil); resp.StatusCode != 401 {
		t.Fatalf("no bearer status = %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", s.URL+"/sites/", access, false, nil); resp.StatusCode != 400 {
		t.Fatalf("no app version status = %d, want 400", resp.StatusCode)
	}
	resp, b := do(t, "GET", s.URL+"/sites/", access, true, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var sites []map[string]any
	if err := json.Unmarshal(b, &sites); err != nil || len(sites) != 1 {
		t.Fatalf("sites = %s (err %v)", b, err)
	}
}

func TestRefreshRotates(t *testing.T) {
	s := New()
	defer s.Close()
	_, refresh := login(t, s)
	resp, b := do(t, "POST", s.URL+"/refresh", "", true, map[string]string{"refresh": refresh})
	if resp.StatusCode != 200 {
		t.Fatalf("refresh status = %d", resp.StatusCode)
	}
	var out struct{ Access, Refresh string }
	json.Unmarshal(b, &out)
	if out.Refresh == "" || out.Refresh == refresh {
		t.Fatalf("refresh token not rotated: %q", out.Refresh)
	}
	if resp, _ := do(t, "POST", s.URL+"/refresh", "", true, map[string]string{"refresh": refresh}); resp.StatusCode != 401 {
		t.Fatalf("reused refresh status = %d, want 401", resp.StatusCode)
	}
	if s.Refreshes() != 1 {
		t.Fatalf("Refreshes() = %d, want 1", s.Refreshes())
	}
}

func TestSendCommandOffSetsPowerZero(t *testing.T) {
	s := New()
	defer s.Close()
	access, _ := login(t, s)
	send := func(cmds map[string]any) {
		resp, _ := do(t, "POST", s.URL+"/devices/send-command", access, true, map[string]any{"deviceSerial": OfficeSerial, "commands": cmds})
		if resp.StatusCode != 200 {
			t.Fatalf("send-command status = %d", resp.StatusCode)
		}
	}
	send(map[string]any{"operationMode": "off"})
	if got := fmt.Sprint(s.Device(OfficeSerial)["power"]); got != "0" {
		t.Fatalf("power = %s, want 0", got)
	}
	send(map[string]any{"operationMode": "heat", "spHeat": 21})
	d := s.Device(OfficeSerial)
	if fmt.Sprint(d["power"]) != "1" || fmt.Sprint(d["spHeat"]) != "21" {
		t.Fatalf("power=%v spHeat=%v", d["power"], d["spHeat"])
	}
	if n := len(s.Commands()); n != 2 {
		t.Fatalf("Commands() has %d entries, want 2", n)
	}
	resp, _ := do(t, "POST", s.URL+"/devices/send-command", access, true, map[string]any{"deviceSerial": "NOPE", "commands": map[string]any{}})
	if resp.StatusCode != 404 {
		t.Fatalf("unknown serial status = %d, want 404", resp.StatusCode)
	}
}

func TestSensor404(t *testing.T) {
	s := New()
	defer s.Close()
	access, _ := login(t, s)
	if resp, _ := do(t, "GET", s.URL+"/devices/"+OfficeSerial+"/sensor", access, true, nil); resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFailNext(t *testing.T) {
	s := New()
	defer s.Close()
	access, _ := login(t, s)
	s.FailNext("/devices/"+OfficeSerial, 429, 1)
	if resp, _ := do(t, "GET", s.URL+"/devices/"+OfficeSerial, access, true, nil); resp.StatusCode != 429 {
		t.Fatalf("first status = %d, want 429", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", s.URL+"/devices/"+OfficeSerial, access, true, nil); resp.StatusCode != 200 {
		t.Fatalf("second status = %d, want 200", resp.StatusCode)
	}
}
