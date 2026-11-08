// SPDX-License-Identifier: GPL-3.0-or-later

// Package kumo is a client for the unofficial Kumo Cloud (Mitsubishi Comfort) API.
package kumo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL    = "https://app-prod.kumocloud.com/v3"
	DefaultAppVersion = "3.2.4"
)

var (
	ErrAuth         = errors.New("kumo: credentials rejected")
	ErrLoginBackoff = errors.New("kumo: login backing off")
)

// Options configures a Client.
type Options struct {
	BaseURL         string
	AppVersion      string
	CredentialsPath string // required
	TokenPath       string // "" = don't persist
	HTTP            *http.Client
	Now             func() time.Time
	Sleep           func(ctx context.Context, d time.Duration) error
	// OnRequest, when set, is told about every request to the cloud: the
	// method, the path with ids folded to {id}, the status (0 when the request
	// failed before one came back) and how long it took.
	OnRequest func(method, route string, status int, took time.Duration)
}

// Client talks to Kumo Cloud. It is safe for concurrent use.
type Client struct {
	o Options

	mu           sync.Mutex // guards all fields below
	loaded       bool
	access       string
	refresh      string
	obtainedAt   time.Time
	logins       int
	refreshes    int
	blockedUntil time.Time
	failBackoff  time.Duration
}

// New returns a Client with defaults filled in.
func New(o Options) *Client {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.AppVersion == "" {
		o.AppVersion = DefaultAppVersion
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &Client{o: o}
}

func (c *Client) LoginCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logins
}

func (c *Client) RefreshCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshes
}

// url joins path to the base. Paths starting with /v4/ go to the v4 API
// (schedules live there), which sits beside /v3 on the same host.
func (c *Client) url(path string) string {
	if strings.HasPrefix(path, "/v4/") {
		return strings.TrimSuffix(c.o.BaseURL, "/v3") + path
	}
	return c.o.BaseURL + path
}

// roundTrip sends one request and returns status and body. Errors never
// include the response body.
func (c *Client) roundTrip(ctx context.Context, method, path, bearer string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("x-app-version", c.o.AppVersion)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	start := time.Now()
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		c.observe(method, path, 0, start)
		return 0, nil, err
	}
	defer resp.Body.Close()
	c.observe(method, path, resp.StatusCode, start)
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, b, nil
}

func (c *Client) observe(method, path string, status int, start time.Time) {
	if c.o.OnRequest != nil {
		c.o.OnRequest(method, Route(path), status, time.Since(start))
	}
}

// Route folds the ids out of a request path so requests group by endpoint:
// any segment with a digit and eight or more characters (serials, uuids)
// becomes {id}.
func Route(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if len(s) >= 8 && strings.ContainsAny(s, "0123456789") {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}
