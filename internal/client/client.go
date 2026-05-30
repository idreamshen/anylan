package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/tap"
	"github.com/idreamshen/anylan/internal/webui"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

const DefaultWebAddr = "127.0.0.1:8081"

type Config struct {
	Server             string
	Room               string
	DisplayName        string
	DeviceName         string
	InsecureSkipVerify bool
	WebAddr            string
	WebToken           string
}

type JoinRequest struct {
	Server             string `json:"server"`
	Room               string `json:"room"`
	DisplayName        string `json:"display_name"`
	DeviceName         string `json:"device_name"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

func Run(ctx context.Context, cfg Config) error {
	if err := normalizeConfig(&cfg); err != nil {
		return err
	}

	status := newStatus(cfg)
	if cfg.WebAddr != "" {
		go func() {
			err := webui.Server{
				Addr:     cfg.WebAddr,
				Title:    "anylan client",
				Token:    cfg.WebToken,
				Snapshot: func() any { return status.Snapshot() },
			}.ListenAndServe(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("web UI error: %v", err)
			}
		}()
	}
	return runLoop(ctx, cfg, status)
}

func RunControl(ctx context.Context, webAddr, webToken string) error {
	if webAddr == "" {
		webAddr = DefaultWebAddr
	}
	controller := NewController(ctx)
	return webui.Server{
		Addr:     webAddr,
		Title:    "anylan client",
		Token:    webToken,
		Snapshot: func() any { return controller.Snapshot() },
		Devices:  controller.Devices,
		Join:     controller.Join,
		Leave:    controller.Leave,
	}.ListenAndServe(ctx)
}

func normalizeConfig(cfg *Config) error {
	cfg.Server = strings.TrimSpace(cfg.Server)
	if cfg.Server == "" {
		return fmt.Errorf("server is required")
	}
	cfg.Room = strings.TrimSpace(cfg.Room)
	if cfg.Room == "" {
		return fmt.Errorf("room is required")
	}
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.DeviceName = strings.TrimSpace(cfg.DeviceName)
	if cfg.DeviceName == "" {
		cfg.DeviceName = tap.DefaultDeviceName()
	}
	return nil
}

func configFromJoinRequest(req JoinRequest) Config {
	return Config{
		Server:             req.Server,
		Room:               req.Room,
		DisplayName:        req.DisplayName,
		DeviceName:         req.DeviceName,
		InsecureSkipVerify: req.InsecureSkipVerify,
	}
}

type Controller struct {
	ctx    context.Context
	mu     sync.Mutex
	status *Status
	cancel context.CancelFunc
	done   chan struct{}
}

func NewController(ctx context.Context) *Controller {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := Config{DeviceName: tap.DefaultDeviceName()}
	return &Controller{ctx: ctx, status: newIdleStatus(cfg)}
}

func (c *Controller) Snapshot() Snapshot {
	return c.status.Snapshot()
}

func (c *Controller) Devices(context.Context, json.RawMessage) (any, error) {
	devices, err := tap.ListDevices()
	if err != nil {
		return nil, err
	}
	return devices, nil
}

func (c *Controller) Join(_ context.Context, payload json.RawMessage) (any, error) {
	var req JoinRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, webui.APIError{Status: http.StatusBadRequest, Message: "invalid join request"}
		}
	}
	cfg := configFromJoinRequest(req)
	if err := normalizeConfig(&cfg); err != nil {
		return nil, webui.APIError{Status: http.StatusBadRequest, Message: err.Error()}
	}

	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil, webui.APIError{Status: http.StatusConflict, Message: "already joined; leave the current room first"}
	}
	sessionCtx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	c.cancel = cancel
	c.done = done
	c.status.startSession(cfg)
	c.mu.Unlock()

	go func() {
		defer close(done)
		err := runLoop(sessionCtx, cfg, c.status)
		c.mu.Lock()
		if c.done == done {
			c.cancel = nil
			c.done = nil
		}
		c.mu.Unlock()
		if sessionCtx.Err() != nil {
			c.status.endSession(nil, "idle")
			return
		}
		if err != nil {
			log.Printf("client session stopped: %v", err)
		}
	}()

	return c.status.Snapshot(), nil
}

func (c *Controller) Leave(context.Context, json.RawMessage) (any, error) {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel == nil {
		return c.status.Snapshot(), nil
	}
	c.status.setState("leaving")
	cancel()
	return c.status.Snapshot(), nil
}

func runLoop(ctx context.Context, cfg Config, status *Status) error {

	backoff := time.Second
	for {
		status.setState("connecting")
		err := runSession(ctx, cfg, status)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isFatal(err) {
			status.endSession(err, "stopped")
			return err
		}
		status.endSession(err, "reconnecting")
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
		var certErr *tls.CertificateVerificationError
		var unknownAuthority x509.UnknownAuthorityError
		if errors.As(err, &certErr) || errors.As(err, &unknownAuthority) {
			return fatalf("TLS certificate verification failed: %w", err)
		}
		return err
	}
	defer conn.CloseWithError(0, "")

	controlStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer controlStream.Close()

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
	if err := protocol.WriteJSON(controlStream, protocol.TypeJoinRoom, join); err != nil {
		return err
	}

	typ, payload, err := protocol.ReadMessage(controlStream, protocol.MaxControlSize)
	if err != nil {
		return err
	}
	if typ == protocol.TypeJoinReject {
		var reject protocol.JoinReject
		if err := decodeJSON(payload, &reject); err != nil {
			return fatalf("decode join reject: %w", err)
		}
		return fatalf("join rejected: %s", reject.Reason)
	}
	if typ != protocol.TypeJoinAccept {
		return fatalf("unexpected join response type %d", typ)
	}
	var accept protocol.JoinAccept
	if err := decodeJSON(payload, &accept); err != nil {
		return fatalf("decode join accept: %w", err)
	}
	status.joined(accept)

	device, err := tap.Open(cfg.DeviceName)
	if err != nil {
		return fatalf("open TAP device: %w", err)
	}
	defer device.Close()

	if err := tap.Configure(ctx, device.Name(), accept.MAC, accept.CIDR, accept.MTU); err != nil {
		return fatalf("configure TAP device: %w", err)
	}
	log.Printf("joined room %q as peer %s on %s (%s, %s)", accept.Room, accept.PeerID, device.Name(), accept.CIDR, accept.MAC)

	dataStream, err := conn.AcceptStream(ctx)
	if err != nil {
		return err
	}
	defer dataStream.Close()

	errCh := make(chan error, 3)
	go readTAP(device, dataStream, status, errCh)
	go writeTAP(device, dataStream, status, errCh)
	go readControl(controlStream, status, errCh)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

type Status struct {
	mu sync.Mutex

	server             string
	room               string
	displayName        string
	deviceName         string
	insecureSkipVerify bool
	state              string
	lastError          string

	peerID        string
	ipv4          string
	cidr          string
	mac           string
	mtu           int
	joinedAt      time.Time
	updatedAt     time.Time
	roomCreatedAt time.Time
	peers         []protocol.PeerInfo

	rxBytes    atomic.Uint64
	rxFrames   atomic.Uint64
	txBytes    atomic.Uint64
	txFrames   atomic.Uint64
	reconnects atomic.Uint64
}

type Snapshot struct {
	GeneratedAt        time.Time           `json:"generated_at"`
	Server             string              `json:"server"`
	Room               string              `json:"room"`
	DisplayName        string              `json:"display_name,omitempty"`
	DeviceName         string              `json:"device_name,omitempty"`
	InsecureSkipVerify bool                `json:"insecure_skip_verify"`
	State              string              `json:"state"`
	LastError          string              `json:"last_error,omitempty"`
	PeerID             string              `json:"peer_id,omitempty"`
	IPv4               string              `json:"ipv4,omitempty"`
	CIDR               string              `json:"cidr,omitempty"`
	MAC                string              `json:"mac,omitempty"`
	MTU                int                 `json:"mtu,omitempty"`
	JoinedAt           time.Time           `json:"joined_at,omitempty"`
	RoomCreatedAt      time.Time           `json:"room_created_at,omitempty"`
	Peers              []protocol.PeerInfo `json:"peers,omitempty"`
	RxBytes            uint64              `json:"rx_bytes"`
	RxFrames           uint64              `json:"rx_frames"`
	TxBytes            uint64              `json:"tx_bytes"`
	TxFrames           uint64              `json:"tx_frames"`
	Reconnects         uint64              `json:"reconnects"`
}

func newStatus(cfg Config) *Status {
	return &Status{
		server:             cfg.Server,
		room:               cfg.Room,
		displayName:        cfg.DisplayName,
		deviceName:         cfg.DeviceName,
		insecureSkipVerify: cfg.InsecureSkipVerify,
		state:              "starting",
		updatedAt:          time.Now(),
	}
}

func newIdleStatus(cfg Config) *Status {
	status := newStatus(cfg)
	status.state = "idle"
	return status
}

func (s *Status) startSession(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.server = cfg.Server
	s.room = cfg.Room
	s.displayName = cfg.DisplayName
	s.deviceName = cfg.DeviceName
	s.insecureSkipVerify = cfg.InsecureSkipVerify
	s.state = "starting"
	s.lastError = ""
	s.peerID = ""
	s.ipv4 = ""
	s.cidr = ""
	s.mac = ""
	s.mtu = 0
	s.joinedAt = time.Time{}
	s.roomCreatedAt = time.Time{}
	s.peers = nil
	s.rxBytes.Store(0)
	s.rxFrames.Store(0)
	s.txBytes.Store(0)
	s.txFrames.Store(0)
	s.reconnects.Store(0)
	s.updatedAt = time.Now()
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
	s.roomCreatedAt = accept.RoomCreatedAt
	s.peers = accept.Peers
	s.joinedAt = time.Now()
	s.updatedAt = s.joinedAt
}

func (s *Status) updatePeers(pl protocol.PeerList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomCreatedAt = pl.RoomCreatedAt
	s.peers = pl.Peers
}

func (s *Status) endSession(err error, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if err != nil {
		s.lastError = err.Error()
	}
	s.peerID = ""
	s.ipv4 = ""
	s.cidr = ""
	s.mac = ""
	s.mtu = 0
	s.joinedAt = time.Time{}
	s.roomCreatedAt = time.Time{}
	s.peers = nil
	s.updatedAt = time.Now()
}

type fatalError struct {
	err error
}

func (e fatalError) Error() string {
	return e.err.Error()
}

func (e fatalError) Unwrap() error {
	return e.err
}

func fatalf(format string, args ...any) error {
	return fatalError{err: fmt.Errorf(format, args...)}
}

func isFatal(err error) bool {
	var fatal fatalError
	return errors.As(err, &fatal)
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
		GeneratedAt:        time.Now(),
		Server:             s.server,
		Room:               s.room,
		DisplayName:        s.displayName,
		DeviceName:         s.deviceName,
		InsecureSkipVerify: s.insecureSkipVerify,
		State:              s.state,
		LastError:          s.lastError,
		PeerID:             s.peerID,
		IPv4:               s.ipv4,
		CIDR:               s.cidr,
		MAC:                s.mac,
		MTU:                s.mtu,
		JoinedAt:           s.joinedAt,
		RoomCreatedAt:      s.roomCreatedAt,
		Peers:              s.peers,
		RxBytes:            s.rxBytes.Load(),
		RxFrames:           s.rxFrames.Load(),
		TxBytes:            s.txBytes.Load(),
		TxFrames:           s.txFrames.Load(),
		Reconnects:         s.reconnects.Load(),
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
		switch typ {
		case protocol.TypeEthernetFrame:
			if _, err := device.Write(payload); err != nil {
				errCh <- err
				return
			}
			status.recordRx(len(payload))
		}
	}
}

func readControl(stream io.Reader, status *Status, errCh chan<- error) {
	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
		if err != nil {
			errCh <- err
			return
		}
		switch typ {
		case protocol.TypePeerList:
			var pl protocol.PeerList
			if err := json.Unmarshal(payload, &pl); err == nil {
				status.updatePeers(pl)
			}
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
