package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Version        = 1
	DefaultMTU     = 1300
	MaxFrameSize   = 1600
	MaxControlSize = 64 * 1024
)

type MessageType byte

const (
	TypeJoinRoom MessageType = iota + 1
	TypeJoinAccept
	TypeJoinReject
	TypeEthernetFrame
	TypePing
	TypePong
)

var (
	ErrPayloadTooLarge = errors.New("payload too large")
	ErrUnknownMessage  = errors.New("unknown message type")
)

type JoinRoom struct {
	Version     int    `json:"version"`
	Room        string `json:"room"`
	DisplayName string `json:"display_name,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
}

type JoinAccept struct {
	Version int    `json:"version"`
	Room    string `json:"room"`
	PeerID  string `json:"peer_id"`
	IPv4    string `json:"ipv4"`
	CIDR    string `json:"cidr"`
	MAC     string `json:"mac"`
	MTU     int    `json:"mtu"`
}

type JoinReject struct {
	Reason string `json:"reason"`
}

func WriteJSON(w io.Writer, typ MessageType, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteMessage(w, typ, payload)
}

func ReadJSON(r io.Reader, want MessageType, maxPayload int, v any) error {
	typ, payload, err := ReadMessage(r, maxPayload)
	if err != nil {
		return err
	}
	if typ != want {
		return fmt.Errorf("expected message %d, got %d", want, typ)
	}
	return json.Unmarshal(payload, v)
}

func WriteMessage(w io.Writer, typ MessageType, payload []byte) error {
	if !typ.Valid() {
		return ErrUnknownMessage
	}
	if typ == TypeEthernetFrame && len(payload) > MaxFrameSize {
		return ErrPayloadTooLarge
	}
	if len(payload) > MaxControlSize {
		return ErrPayloadTooLarge
	}

	var header [5]byte
	header[0] = byte(typ)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func ReadMessage(r io.Reader, maxPayload int) (MessageType, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	typ := MessageType(header[0])
	if !typ.Valid() {
		return 0, nil, ErrUnknownMessage
	}

	size := int(binary.BigEndian.Uint32(header[1:]))
	if maxPayload > 0 && size > maxPayload {
		return 0, nil, ErrPayloadTooLarge
	}
	if typ == TypeEthernetFrame && size > MaxFrameSize {
		return 0, nil, ErrPayloadTooLarge
	}

	payload := make([]byte, size)
	if size == 0 {
		return typ, payload, nil
	}
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}

func (t MessageType) Valid() bool {
	return t >= TypeJoinRoom && t <= TypePong
}
