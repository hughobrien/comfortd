// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// DefaultSocketURL is the Comfort app's push server.
const DefaultSocketURL = "https://socket-prod.kumocloud.com"

// Event is one Socket.IO event: its name and first argument.
type Event struct {
	Name string
	Data json.RawMessage
}

// SocketOptions configures a Socket.
type SocketOptions struct {
	URL        string   // http(s) or ws(s) base, default DefaultSocketURL
	Serials    []string // units to subscribe to after every (re)connect
	OnEvent    func(Event)
	OnState    func(connected bool) // called when a session becomes ready and when it ends
	MinBackoff time.Duration        // default 1 s
	MaxBackoff time.Duration        // default 60 s
	RefreshGap time.Duration        // least time between forced token refreshes, default 30 s
}

// Socket is a read-only Socket.IO client for the push feed. It sends only the
// namespace connect, pongs and `subscribe <serial>`.
type Socket struct {
	c *Client
	o SocketOptions

	reconnects atomic.Int64
	subscribed atomic.Int64 // `subscribed` replies in the current session
	lastForce  time.Time
}

var errUnauthorised = errors.New("kumo socket: unauthorised")

// NewSocket returns a Socket that authenticates with c's tokens.
func (c *Client) NewSocket(o SocketOptions) *Socket {
	if o.URL == "" {
		o.URL = DefaultSocketURL
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = time.Minute
	}
	if o.RefreshGap == 0 {
		o.RefreshGap = 30 * time.Second
	}
	return &Socket{c: c, o: o}
}

// Reconnects is how many times the socket has had to reconnect.
func (s *Socket) Reconnects() int { return int(s.reconnects.Load()) }

// Expected is how many subscriptions each session asks for.
func (s *Socket) Expected() int { return len(s.o.Serials) }

// Subscribed is how many `subscribed` replies the current session has had.
func (s *Socket) Subscribed() int { return int(s.subscribed.Load()) }

func (s *Socket) wsURL() string {
	u := strings.TrimRight(s.o.URL, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + u[len("https://"):]
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + u[len("http://"):]
	}
	return u + "/socket.io/?EIO=4&transport=websocket"
}

// Run keeps a session going until ctx is done, reconnecting with backoff.
func (s *Socket) Run(ctx context.Context) error {
	backoff := s.o.MinBackoff
	first := true
	for ctx.Err() == nil {
		if !first {
			s.reconnects.Add(1)
		}
		first = false
		started := time.Now()
		err := s.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, errUnauthorised) {
			s.forceRefresh(ctx)
		}
		slog.Warn("kumo socket ended", "err", err, "retry_in", backoff)
		if time.Since(started) > time.Minute {
			backoff = s.o.MinBackoff
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.o.MaxBackoff)
	}
	return ctx.Err()
}

// forceRefresh refreshes the token, but not more often than RefreshGap.
func (s *Socket) forceRefresh(ctx context.Context) {
	if time.Since(s.lastForce) < s.o.RefreshGap {
		return
	}
	s.lastForce = time.Now()
	if _, err := s.c.token(ctx, true); err != nil {
		slog.Warn("kumo socket token refresh failed", "err", err)
	}
}

func (s *Socket) session(ctx context.Context) (err error) {
	tok, err := s.c.token(ctx, false)
	if err != nil {
		return err
	}
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	defer dcancel()
	ws, resp, err := websocket.Dial(dctx, s.wsURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
	})
	if err != nil {
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			return errUnauthorised
		}
		return errors.New("dial failed") // never include the URL or headers
	}
	defer ws.CloseNow()
	defer func() {
		if n := int(s.subscribed.Load()); n < len(s.o.Serials) {
			slog.Warn("kumo socket session ended with missing subscriptions", "confirmed", n, "asked", len(s.o.Serials))
		}
	}()
	ws.SetReadLimit(4 << 20)
	s.subscribed.Store(0)
	ready := false
	defer func() {
		if ready && s.o.OnState != nil {
			s.o.OnState(false)
		}
	}()

	var wait time.Duration = 45 * time.Second // until the open packet says otherwise
	write := func(frame string) error {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return ws.Write(wctx, websocket.MessageText, []byte(frame))
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, wait)
		_, msg, rerr := ws.Read(rctx)
		cancel()
		if rerr != nil {
			if ctx.Err() == nil && errors.Is(rerr, context.DeadlineExceeded) {
				return errors.New("no ping from the server")
			}
			return rerr
		}
		p, perr := parsePacket(string(msg))
		if perr != nil {
			slog.Debug("kumo socket: bad packet", "err", perr)
			continue
		}
		switch p.Eio {
		case '0':
			var open struct {
				PingInterval int64 `json:"pingInterval"`
				PingTimeout  int64 `json:"pingTimeout"`
			}
			if json.Unmarshal(p.Data, &open) == nil && open.PingInterval > 0 {
				wait = time.Duration(open.PingInterval+open.PingTimeout) * time.Millisecond
			}
			if err := write(encodeConnect()); err != nil {
				return err
			}
		case '2':
			if err := write(encodePong()); err != nil {
				return err
			}
		case '1':
			return errors.New("server closed the session")
		case '4':
			switch p.Sio {
			case '0':
				for _, ser := range s.o.Serials {
					f, _ := encodeEvent("subscribe", ser)
					if err := write(f); err != nil {
						return err
					}
				}
				ready = true
				if s.o.OnState != nil {
					s.o.OnState(true)
				}
			case '4':
				return fmt.Errorf("%w (connect error)", errUnauthorised)
			case '1':
				return errors.New("server disconnected the namespace")
			case '2':
				if p.Event == "subscribed" {
					s.subscribed.Add(1)
					continue
				}
				if s.o.OnEvent != nil {
					var data json.RawMessage
					if len(p.Args) > 0 {
						data = p.Args[0]
					}
					s.o.OnEvent(Event{Name: p.Event, Data: data})
				}
			}
		}
	}
}
