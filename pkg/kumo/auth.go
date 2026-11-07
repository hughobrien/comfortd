// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hughobrien/comfortd/internal/fsutil"
)

const (
	accessMaxAge  = 15 * time.Minute
	authRejectGap = 15 * time.Minute
	minBackoff    = 30 * time.Second
	maxBackoff    = 15 * time.Minute
)

type storedTokens struct {
	Access     string    `json:"access"`
	Refresh    string    `json:"refresh"`
	ObtainedAt time.Time `json:"obtainedAt"`
}

// token returns a usable access token: stored token if fresh, else refresh,
// else login. forceRefresh skips the freshness shortcut.
func (c *Client) token(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.loaded {
		c.loaded = true
		c.loadStored()
	}
	now := c.o.Now()
	if !forceRefresh && c.access != "" && now.Sub(c.obtainedAt) < accessMaxAge {
		return c.access, nil
	}
	if c.refresh != "" {
		err := c.doRefresh(ctx)
		if err == nil {
			return c.access, nil
		}
		slog.Warn("kumo refresh failed", "err", err)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if err := c.doLogin(ctx); err != nil {
		return "", err
	}
	return c.access, nil
}

func (c *Client) loadStored() {
	if c.o.TokenPath == "" {
		return
	}
	b, err := os.ReadFile(c.o.TokenPath)
	if err != nil {
		return
	}
	var st storedTokens
	if json.Unmarshal(b, &st) != nil {
		return
	}
	c.access, c.refresh, c.obtainedAt = st.Access, st.Refresh, st.ObtainedAt
}

func (c *Client) setTokens(access, refresh string) {
	c.access, c.refresh, c.obtainedAt = access, refresh, c.o.Now()
	if c.o.TokenPath == "" {
		return
	}
	b, _ := json.Marshal(storedTokens{Access: c.access, Refresh: c.refresh, ObtainedAt: c.obtainedAt})
	if err := fsutil.WriteFileAtomic(c.o.TokenPath, b); err != nil {
		slog.Warn("kumo: could not persist tokens", "err", err)
	}
}

// parseTokens accepts {access, refresh} and {token:{access, refresh}}.
func parseTokens(b []byte) (access, refresh string, err error) {
	var v struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
		Token   struct {
			Access  string `json:"access"`
			Refresh string `json:"refresh"`
		} `json:"token"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", "", errors.New("kumo: unparseable token response")
	}
	access, refresh = v.Access, v.Refresh
	if access == "" {
		access, refresh = v.Token.Access, v.Token.Refresh
	}
	if access == "" || refresh == "" {
		return "", "", errors.New("kumo: token response missing access or refresh")
	}
	return access, refresh, nil
}

// doRefresh is called with c.mu held.
func (c *Client) doRefresh(ctx context.Context) error {
	status, body, err := c.roundTrip(ctx, "POST", "/refresh", "", map[string]string{"refresh": c.refresh})
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("refresh: status %d", status)
	}
	a, r, err := parseTokens(body)
	if err != nil {
		return err
	}
	c.setTokens(a, r)
	c.refreshes++
	slog.Info("kumo refresh", "count", c.refreshes)
	return nil
}

// doLogin is called with c.mu held.
func (c *Client) doLogin(ctx context.Context) error {
	now := c.o.Now()
	if now.Before(c.blockedUntil) {
		return fmt.Errorf("%w until %s", ErrLoginBackoff, c.blockedUntil.Format(time.RFC3339))
	}
	user, pass, err := ReadCredentials(c.o.CredentialsPath)
	if err != nil {
		return err
	}
	status, body, err := c.roundTrip(ctx, "POST", "/login", "", map[string]string{
		"username": user, "password": pass, "appVersion": c.o.AppVersion,
	})
	c.logins++
	if err == nil && (status == 401 || status == 403) {
		c.blockedUntil = now.Add(authRejectGap)
		slog.Warn("kumo login failed", "err", ErrAuth)
		return fmt.Errorf("%w (status %d)", ErrAuth, status)
	}
	if err == nil && status != 200 {
		err = fmt.Errorf("login: status %d", status)
	}
	var a, r string
	if err == nil {
		a, r, err = parseTokens(body)
	}
	if err != nil {
		if c.failBackoff == 0 {
			c.failBackoff = minBackoff
		} else if c.failBackoff *= 2; c.failBackoff > maxBackoff {
			c.failBackoff = maxBackoff
		}
		c.blockedUntil = now.Add(c.failBackoff)
		slog.Warn("kumo login failed", "err", err)
		return err
	}
	c.failBackoff = 0
	c.blockedUntil = time.Time{}
	c.setTokens(a, r)
	slog.Info("kumo login", "count", c.logins)
	return nil
}
