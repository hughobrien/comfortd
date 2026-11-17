// SPDX-License-Identifier: GPL-3.0-or-later

package kumotest

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// sockState is the fake Socket.IO side of the server (Engine.IO v4 over a
// websocket, default namespace), guarded by Server.mu.
type sockState struct {
	conns       map[*sockConn]struct{}
	connects    int
	subscribed  []string // every serial any client subscribed to, in order
	rejectUp    int      // answer the next upgrades with this HTTP status
	rejectConn  int      // answer the next namespace connects with a 44 packet
	pingEvery   time.Duration
	pingTimeout time.Duration
	silent      bool // stop sending pings
}

type sockConn struct {
	out  chan string
	subs map[string]bool
	stop context.CancelFunc
}

// SetPing sets the Engine.IO ping interval and timeout the server announces.
func (s *Server) SetPing(every, timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sock.pingEvery, s.sock.pingTimeout = every, timeout
}

// SilenceServer stops (or resumes) the server's pings, so a client should
// notice and reconnect.
func (s *Server) SilenceServer(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sock.silent = v
}

// RejectUpgrade makes the next n websocket upgrades fail with status.
func (s *Server) RejectUpgrade(status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sock.rejectUp = status*1000 + n
}

// RejectConnect makes the next n namespace connects answer 44 (connect error).
func (s *Server) RejectConnect(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sock.rejectConn = n
}

// DisconnectSockets closes every socket client's connection.
func (s *Server) DisconnectSockets() {
	s.mu.Lock()
	var stops []context.CancelFunc
	for c := range s.sock.conns {
		stops = append(stops, c.stop)
	}
	s.mu.Unlock()
	for _, f := range stops {
		f()
	}
}

// SocketConnects is how many socket handshakes have completed.
func (s *Server) SocketConnects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sock.connects
}

// SocketSubscribed lists every serial subscribed to so far, in order.
func (s *Server) SocketSubscribed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sock.subscribed)
}

// SocketClients is how many socket clients are connected now.
func (s *Server) SocketClients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sock.conns)
}

// push queues one event frame to every client subscribed to serial.
func (s *Server) push(serial, name string, body map[string]any) {
	b, _ := json.Marshal([]any{name, body})
	frame := "42" + string(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.sock.conns {
		if c.subs[serial] {
			select {
			case c.out <- frame:
			default:
			}
		}
	}
}

var fastKeys = []string{"rssi", "roomTemp", "operationMode", "power", "spCool", "spHeat", "airDirection", "fanSpeed", "connected", "realValues"}

// PushDevice pushes a device_update for serial built from the live device: the
// whole record ("full") or just the live fields ("fast").
func (s *Server) PushDevice(serial string, full bool) {
	s.mu.Lock()
	dev, ok := s.devices[serial]
	var body map[string]any
	if ok {
		body = copyMap(dev)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if full {
		body["payloadType"], body["date"] = "full", now
		s.push(serial, "device_update", body)
		return
	}
	fast := map[string]any{"deviceSerial": serial, "payloadType": "fast", "date": now}
	for _, k := range fastKeys {
		if v, ok := body[k]; ok {
			fast[k] = v
		}
	}
	if dc, ok := body["displayConfig"]; ok {
		fast["displayConfigFast"] = dc
	}
	s.push(serial, "device_update", fast)
}

// PushHoldUpdate pushes the hold the fake holds for zone to its unit's
// subscribers, twice as the real server does.
func (s *Server) PushHoldUpdate(zone string) {
	serial := OfficeSerial
	if zone == HouseZoneID {
		serial = HouseSerial
	}
	s.mu.Lock()
	h, ok := s.holds[zone]
	var body map[string]any
	if ok {
		body = copyMap(h)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	body["deviceSerial"] = serial
	body["date"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	s.push(serial, "hold_update", body)
	s.push(serial, "hold_update", body)
}

func (s *Server) handleSocket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	authorised := s.access != "" && r.Header.Get("Authorization") == "Bearer "+s.access && !s.expired[s.access]
	rej := 0
	if s.sock.rejectUp%1000 > 0 {
		rej = s.sock.rejectUp / 1000
		s.sock.rejectUp--
	}
	every, timeout := s.sock.pingEvery, s.sock.pingTimeout
	s.mu.Unlock()
	if rej != 0 {
		w.WriteHeader(rej)
		return
	}
	if !authorised {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if every == 0 {
		every, timeout = 25*time.Second, 20*time.Second
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &sockConn{out: make(chan string, 64), subs: map[string]bool{}, stop: cancel}
	defer ws.CloseNow()

	open, _ := json.Marshal(map[string]any{"sid": "fakesid", "upgrades": []string{}, "pingInterval": every.Milliseconds(), "pingTimeout": timeout.Milliseconds()})
	if ws.Write(ctx, websocket.MessageText, append([]byte("0"), open...)) != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // writer: queued frames and pings
		defer wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-c.out:
				if ws.Write(ctx, websocket.MessageText, []byte(f)) != nil {
					cancel()
					return
				}
			case <-t.C:
				s.mu.Lock()
				silent := s.sock.silent
				s.mu.Unlock()
				if !silent && ws.Write(ctx, websocket.MessageText, []byte("2")) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		wg.Wait()
		s.mu.Lock()
		delete(s.sock.conns, c)
		s.mu.Unlock()
	}()
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			return
		}
		m := string(msg)
		switch {
		case m == "40":
			s.mu.Lock()
			reject := s.sock.rejectConn > 0
			if reject {
				s.sock.rejectConn--
			}
			s.mu.Unlock()
			if reject {
				c.out <- `44{"message":"Unauthorized"}`
				continue
			}
			s.mu.Lock()
			if s.sock.conns == nil {
				s.sock.conns = map[*sockConn]struct{}{}
			}
			s.sock.conns[c] = struct{}{}
			s.sock.connects++
			s.mu.Unlock()
			c.out <- `40{"sid":"fakens"}`
		case strings.HasPrefix(m, "42["):
			var arr []json.RawMessage
			if json.Unmarshal(msg[2:], &arr) != nil || len(arr) < 2 {
				continue
			}
			var name, serial string
			json.Unmarshal(arr[0], &name)
			json.Unmarshal(arr[1], &serial)
			if name != "subscribe" {
				continue
			}
			s.mu.Lock()
			_, known := s.devices[serial]
			if known {
				c.subs[serial] = true
				s.sock.subscribed = append(s.sock.subscribed, serial)
			}
			s.mu.Unlock()
			if known {
				b, _ := json.Marshal([]any{"subscribed", serial})
				c.out <- "42" + string(b)
				s.PushDevice(serial, true)
			}
		}
	}
}
