package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/tap"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

type Config struct {
	Server             string
	Room               string
	DisplayName        string
	DeviceName         string
	InsecureSkipVerify bool
}

func Run(ctx context.Context, cfg Config) error {
	if cfg.Server == "" {
		return fmt.Errorf("server is required")
	}
	cfg.Room = strings.TrimSpace(cfg.Room)
	if cfg.Room == "" {
		return fmt.Errorf("room is required")
	}
	if cfg.DeviceName == "" {
		cfg.DeviceName = tap.DefaultDeviceName()
	}

	backoff := time.Second
	for {
		err := runSession(ctx, cfg)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("session ended: %v; reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func runSession(ctx context.Context, cfg Config) error {
	tlsConfig := &tls.Config{
		NextProtos:         []string{alpn},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	conn, err := quic.DialAddr(ctx, cfg.Server, tlsConfig, &quic.Config{
		MaxIdleTimeout: 60 * time.Second,
	})
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	nonce, err := nonceHex()
	if err != nil {
		return err
	}
	join := protocol.JoinRoom{
		Version:     protocol.Version,
		Room:        cfg.Room,
		DisplayName: cfg.DisplayName,
		Nonce:       nonce,
	}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinRoom, join); err != nil {
		return err
	}

	typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
	if err != nil {
		return err
	}
	if typ == protocol.TypeJoinReject {
		var reject protocol.JoinReject
		if err := decodeJSON(payload, &reject); err != nil {
			return err
		}
		return fmt.Errorf("join rejected: %s", reject.Reason)
	}
	if typ != protocol.TypeJoinAccept {
		return fmt.Errorf("unexpected join response type %d", typ)
	}
	var accept protocol.JoinAccept
	if err := decodeJSON(payload, &accept); err != nil {
		return err
	}

	device, err := tap.Open(cfg.DeviceName)
	if err != nil {
		return err
	}
	defer device.Close()

	if err := tap.Configure(ctx, device.Name(), accept.MAC, accept.CIDR, accept.MTU); err != nil {
		return err
	}
	log.Printf("joined room %q as peer %s on %s (%s, %s)", accept.Room, accept.PeerID, device.Name(), accept.CIDR, accept.MAC)

	errCh := make(chan error, 2)
	go readTAP(device, stream, errCh)
	go writeTAP(device, stream, errCh)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func readTAP(device io.Reader, stream io.Writer, errCh chan<- error) {
	buf := make([]byte, protocol.MaxFrameSize)
	for {
		n, err := device.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		if n == 0 {
			continue
		}
		frame := append([]byte(nil), buf[:n]...)
		if err := protocol.WriteMessage(stream, protocol.TypeEthernetFrame, frame); err != nil {
			errCh <- err
			return
		}
	}
}

func writeTAP(device io.Writer, stream io.Reader, errCh chan<- error) {
	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				errCh <- err
				return
			}
			errCh <- err
			return
		}
		if typ != protocol.TypeEthernetFrame {
			continue
		}
		if _, err := device.Write(payload); err != nil {
			errCh <- err
			return
		}
	}
}

func nonceHex() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func decodeJSON(payload []byte, v any) error {
	return json.Unmarshal(payload, v)
}
