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
	"sync"
	"sync/atomic"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/tap"
	"github.com/idreamshen/anylan/internal/webstatus"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

type Config struct {
	Server             string
	Room               string
	DisplayName        string
	DeviceName         string
	InsecureSkipVerify bool
	WebAddr            string
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

	status := newStatus(cfg)
	if cfg.WebAddr != "" {
		go func() {
			err := webstatus.Server{
				Addr:     cfg.WebAddr,
				Title:    "anylan client",
				Snapshot: func() any { return status.Snapshot() },
			}.ListenAndServe(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("web status error: %v", err)
			}
		}()
	}

	backoff := time.Second
	for {
		status.setState("connecting")
		err := runSession(ctx, cfg, status)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status.endSession(err)
		log.Printf("session ended: %v; reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		status.reconnects.Add(1)
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func runSession(ctx context.Context, cfg Config, status *Status) error {
	tlsConfig := &tls.Config{
		NextProtos:         []string{alpn},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	conn, err := quic.DialAddr(ctx, cfg.Server, tlsConfig, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
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
	status.joined(accept)

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
	go readTAP(device, stream, status, errCh)
	go writeTAP(device, stream, status, errCh)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

type Status struct {
	mu sync.Mutex

	server    string
	room      string
	device    string
	state     string
	lastError string

	peerID    string
	ipv4      string
	cidr      string
	mac       string
	mtu       int
	joinedAt  time.Time
	updatedAt time.Time

	rxBytes    atomic.Uint64
	rxFrames   atomic.Uint64
	txBytes    atomic.Uint64
	txFrames   atomic.Uint64
	reconnects atomic.Uint64
}

type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Server      string    `json:"server"`
	Room        string    `json:"room"`
	Device      string    `json:"device"`
	State       string    `json:"state"`
	LastError   string    `json:"last_error,omitempty"`
	PeerID      string    `json:"peer_id,omitempty"`
	IPv4        string    `json:"ipv4,omitempty"`
	CIDR        string    `json:"cidr,omitempty"`
	MAC         string    `json:"mac,omitempty"`
	MTU         int       `json:"mtu,omitempty"`
	JoinedAt    time.Time `json:"joined_at,omitempty"`
	RxBytes     uint64    `json:"rx_bytes"`
	RxFrames    uint64    `json:"rx_frames"`
	TxBytes     uint64    `json:"tx_bytes"`
	TxFrames    uint64    `json:"tx_frames"`
	Reconnects  uint64    `json:"reconnects"`
}

func newStatus(cfg Config) *Status {
	return &Status{
		server:    cfg.Server,
		room:      cfg.Room,
		device:    cfg.DeviceName,
		state:     "starting",
		updatedAt: time.Now(),
	}
}

func (s *Status) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.updatedAt = time.Now()
}

func (s *Status) joined(accept protocol.JoinAccept) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = "connected"
	s.lastError = ""
	s.peerID = accept.PeerID
	s.ipv4 = accept.IPv4
	s.cidr = accept.CIDR
	s.mac = accept.MAC
	s.mtu = accept.MTU
	s.joinedAt = time.Now()
	s.updatedAt = s.joinedAt
}

func (s *Status) endSession(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = "reconnecting"
	if err != nil {
		s.lastError = err.Error()
	}
	s.updatedAt = time.Now()
}

func (s *Status) recordRx(size int) {
	s.rxFrames.Add(1)
	s.rxBytes.Add(uint64(size))
}

func (s *Status) recordTx(size int) {
	s.txFrames.Add(1)
	s.txBytes.Add(uint64(size))
}

func (s *Status) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		GeneratedAt: time.Now(),
		Server:      s.server,
		Room:        s.room,
		Device:      s.device,
		State:       s.state,
		LastError:   s.lastError,
		PeerID:      s.peerID,
		IPv4:        s.ipv4,
		CIDR:        s.cidr,
		MAC:         s.mac,
		MTU:         s.mtu,
		JoinedAt:    s.joinedAt,
		RxBytes:     s.rxBytes.Load(),
		RxFrames:    s.rxFrames.Load(),
		TxBytes:     s.txBytes.Load(),
		TxFrames:    s.txFrames.Load(),
		Reconnects:  s.reconnects.Load(),
	}
}

func readTAP(device io.Reader, stream io.Writer, status *Status, errCh chan<- error) {
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
		status.recordTx(len(frame))
	}
}

func writeTAP(device io.Writer, stream io.Reader, status *Status, errCh chan<- error) {
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
		status.recordRx(len(payload))
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
