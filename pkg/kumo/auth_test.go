// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/pkg/kumo/kumotest"
)

type env struct {
	srv   *kumotest.Server
	dir   string
	creds string
	now   time.Time
}

func newEnv(t *testing.T, password string) *env {
	t.Helper()
	e := &env{srv: kumotest.New(), dir: t.TempDir(), now: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(e.srv.Close)
	e.creds = filepath.Join(e.dir, "credentials")
	if err := os.WriteFile(e.creds, []byte(kumotest.User+"\n"+password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) client() *Client {
	return New(Options{
		BaseURL:         e.srv.URL,
		CredentialsPath: e.creds,
		TokenPath:       filepath.Join(e.dir, "tokens.json"),
		Now:             func() time.Time { return e.now },
		Sleep:           func(context.Context, time.Duration) error { return nil },
	})
}

func TestReadCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c")
	os.WriteFile(p, []byte("a@b\r\npw \n"), 0o600)
	u, pw, err := ReadCredentials(p)
	if err != nil || u != "a@b" || pw != "pw" {
		t.Fatalf("got %q %q %v", u, pw, err)
	}
	os.WriteFile(p, []byte("only-one-line"), 0o600)
	if _, _, err := ReadCredentials(p); err == nil {
		t.Fatal("want error for single line")
	}
}

func TestLoginOnFirstUse(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	tok, err := c.token(context.Background(), false)
	if err != nil || tok == "" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if e.srv.Logins() != 1 {
		t.Fatalf("Logins() = %d, want 1", e.srv.Logins())
	}
	fi, err := os.Stat(filepath.Join(e.dir, "tokens.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("tokens.json: %v %v", fi, err)
	}
}

func TestStoredTokenReused(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	if _, err := e.client().token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(5 * time.Minute)
	if _, err := e.client().token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if e.srv.Logins() != 1 || e.srv.Refreshes() != 0 {
		t.Fatalf("logins=%d refreshes=%d, want 1 and 0", e.srv.Logins(), e.srv.Refreshes())
	}
}

func TestRefreshRotatesAndPersists(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	c := e.client()
	ctx := context.Background()
	c.token(ctx, false)
	e.now = e.now.Add(16 * time.Minute)
	if _, err := c.token(ctx, false); err != nil {
		t.Fatal(err)
	}
	if e.srv.Refreshes() != 1 || e.srv.Logins() != 1 {
		t.Fatalf("refreshes=%d logins=%d", e.srv.Refreshes(), e.srv.Logins())
	}
	b, _ := os.ReadFile(filepath.Join(e.dir, "tokens.json"))
	var st storedTokens
	json.Unmarshal(b, &st)
	if st.Refresh != "refresh-2" {
		t.Fatalf("stored refresh = %q, want refresh-2", st.Refresh)
	}
	e.now = e.now.Add(16 * time.Minute)
	if _, err := c.token(ctx, false); err != nil {
		t.Fatal(err)
	}
	if e.srv.Refreshes() != 2 || e.srv.Logins() != 1 {
		t.Fatalf("second: refreshes=%d logins=%d", e.srv.Refreshes(), e.srv.Logins())
	}
}

func TestRefreshNestedShape(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	e.srv.SetRefreshNested(true)
	c := e.client()
	c.token(context.Background(), false)
	e.now = e.now.Add(16 * time.Minute)
	if _, err := c.token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if e.srv.Refreshes() != 1 || e.srv.Logins() != 1 {
		t.Fatalf("refreshes=%d logins=%d", e.srv.Refreshes(), e.srv.Logins())
	}
}

func TestRefreshFailureFallsBackToLogin(t *testing.T) {
	e := newEnv(t, kumotest.Password)
	stale := storedTokens{Access: "old", Refresh: "unknown", ObtainedAt: e.now.Add(-20 * time.Minute)}
	b, _ := json.Marshal(stale)
	os.WriteFile(filepath.Join(e.dir, "tokens.json"), b, 0o600)
	if _, err := e.client().token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if e.srv.Logins() != 1 {
		t.Fatalf("Logins() = %d, want 1", e.srv.Logins())
	}
}

func TestLoginBackoffAfterAuthReject(t *testing.T) {
	e := newEnv(t, "wrong")
	c := e.client()
	ctx := context.Background()
	if _, err := c.token(ctx, false); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	e.now = e.now.Add(time.Minute)
	if _, err := c.token(ctx, false); !errors.Is(err, ErrLoginBackoff) {
		t.Fatalf("err = %v, want ErrLoginBackoff", err)
	}
	if e.srv.Logins() != 1 {
		t.Fatalf("Logins() = %d, want 1", e.srv.Logins())
	}
	e.now = e.now.Add(15 * time.Minute)
	c.token(ctx, false)
	if e.srv.Logins() != 2 {
		t.Fatalf("Logins() = %d, want 2", e.srv.Logins())
	}
}
