package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello")
	if err := WriteMessage(&buf, TypePing, payload); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	typ, got, err := ReadMessage(&buf, MaxControlSize)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if typ != TypePing {
		t.Fatalf("type = %d, want %d", typ, TypePing)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestRejectsOversizedEthernetFrame(t *testing.T) {
	frame := bytes.Repeat([]byte{0xaa}, MaxFrameSize+1)
	if err := WriteMessage(&bytes.Buffer{}, TypeEthernetFrame, frame); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrPayloadTooLarge)
	}
}

func TestRejectsMalformedMessage(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(99)
	_ = binary.Write(&buf, binary.BigEndian, uint32(1))
	buf.WriteByte(0)

	if _, _, err := ReadMessage(&buf, MaxControlSize); !errors.Is(err, ErrUnknownMessage) {
		t.Fatalf("error = %v, want %v", err, ErrUnknownMessage)
	}
}

func TestRejectsPayloadOverReadLimit(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(byte(TypePing))
	_ = binary.Write(&buf, binary.BigEndian, uint32(10))

	if _, _, err := ReadMessage(&buf, 1); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrPayloadTooLarge)
	}
}
