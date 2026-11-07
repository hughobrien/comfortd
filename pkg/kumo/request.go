// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("kumo: not found")

// HTTPError is a non-success response.
type HTTPError struct {
	Status int
	Path   string
	Method string
}

func (e *HTTPError) Error() string {
	m := e.Method
	if m == "" {
		m = "GET"
	}
	return fmt.Sprintf("kumo: %s %s: status %d", m, e.Path, e.Status)
}

// Get fetches path (e.g. "/sites/") and returns the raw JSON body.
func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.do(ctx, "GET", path, nil)
}

// SendCommand sends a command to a unit. Tests must only point this at kumotest.
func (c *Client) SendCommand(ctx context.Context, serial string, commands map[string]any) error {
	_, err := c.do(ctx, "POST", "/devices/send-command", map[string]any{
		"deviceSerial": serial, "commands": commands,
	})
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	tok, err := c.token(ctx, false)
	if err != nil {
		return nil, err
	}
	refreshed := false
	backoffs := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	retries := 0
	for {
		status, b, err := c.roundTrip(ctx, method, path, tok, body)
		if err != nil {
			return nil, err
		}
		switch {
		case status >= 200 && status < 300:
			return json.RawMessage(b), nil
		case status == 401 && !refreshed:
			refreshed = true
			if tok, err = c.token(ctx, true); err != nil {
				return nil, err
			}
		case status == 429 && retries < len(backoffs):
			if err := c.o.Sleep(ctx, backoffs[retries]); err != nil {
				return nil, err
			}
			retries++
		case status == 404:
			return nil, ErrNotFound
		default:
			return nil, &HTTPError{Status: status, Path: path, Method: method}
		}
	}
}
