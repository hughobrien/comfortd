// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Packet is one Engine.IO v4 text packet with its Socket.IO payload, if any.
// Eio is the Engine.IO type character ('0' open, '1' close, '2' ping, '3' pong,
// '4' message); for a message Sio is the Socket.IO type ('0' connect,
// '1' disconnect, '2' event, '3' ack, '4' connect error). Data is the JSON after
// the type for open, connect and connect-error packets; Event and Args are set
// for events.
type Packet struct {
	Eio, Sio byte
	Data     json.RawMessage
	Event    string
	Args     []json.RawMessage
}

// parsePacket decodes one websocket text frame. It supports the default
// namespace and tolerates an explicit "/ns," prefix and an ack id.
func parsePacket(s string) (Packet, error) {
	if s == "" {
		return Packet{}, errors.New("sio: empty packet")
	}
	p := Packet{Eio: s[0]}
	rest := s[1:]
	switch p.Eio {
	case '0':
		p.Data = json.RawMessage(rest)
		return p, nil
	case '1', '2', '3', '5', '6':
		return p, nil
	case '4':
	default:
		return p, fmt.Errorf("sio: unknown engine packet %q", s[0])
	}
	if rest == "" {
		return p, errors.New("sio: message without a type")
	}
	p.Sio = rest[0]
	rest = rest[1:]
	if strings.HasPrefix(rest, "/") { // namespace prefix up to the comma
		if i := strings.IndexByte(rest, ','); i >= 0 {
			rest = rest[i+1:]
		} else {
			rest = ""
		}
	}
	switch p.Sio {
	case '0', '1', '4':
		if rest != "" {
			p.Data = json.RawMessage(rest)
		}
		return p, nil
	case '2', '3':
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' { // ack id
			j++
		}
		rest = rest[j:]
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(rest), &arr); err != nil {
			return p, fmt.Errorf("sio: event body: %w", err)
		}
		if p.Sio == '3' {
			p.Args = arr
			return p, nil
		}
		if len(arr) == 0 {
			return p, errors.New("sio: event without a name")
		}
		if err := json.Unmarshal(arr[0], &p.Event); err != nil {
			return p, fmt.Errorf("sio: event name: %w", err)
		}
		p.Args = arr[1:]
		return p, nil
	}
	return p, fmt.Errorf("sio: unknown socket.io packet %q", p.Sio)
}

func encodePong() string    { return "3" }
func encodeConnect() string { return "40" }

// encodeEvent builds a Socket.IO event frame, e.g. 42["subscribe","SERIAL"].
func encodeEvent(name string, args ...any) (string, error) {
	b, err := json.Marshal(append([]any{name}, args...))
	if err != nil {
		return "", err
	}
	return "42" + string(b), nil
}
