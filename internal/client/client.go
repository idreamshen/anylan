package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idreamshen/anylan/internal/capture"
	"github.com/idreamshen/anylan/internal/logmem"
	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/tap"
	"github.com/idreamshen/anylan/internal/webui"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

const DefaultWebAddr = "127.0.0.1:18081"

type Config struct {
	Server                   string
	Room                     string
	DisplayName              string
	DeviceName               string
	InsecureSkipVerify       bool
	PrioritizeVirtualAdapter bool
	WebAddr                  string
	WebToken                 string
	Logs                     *logmem.Recorder
	Capture                  *capture.Recorder
}

type JoinRequest struct {
	Server                   string `json:"server"`
	Room                     string `json:"room"`
	DisplayName              string `json:"display_name"`
	DeviceName               string `json:"device_name"`
	InsecureSkipVerify       bool   `json:"insecure_skip_verify"`
	PrioritizeVirtualAdapter bool   `json:"prioritize_virtual_adapter"`
}

func Run(ctx context.Context, cfg Config) error {
	if err := normalizeConfig(&cfg); err != nil {
		return err
	}

	status := newStatus(cfg)
	if cfg.WebAddr != "" && cfg.Capture == nil {
		cfg.Capture = capture.NewRecorder(capture.DefaultLimit)
	}
	if cfg.WebAddr != "" {
		go func() {
			server := webui.Server{
				Addr:     cfg.WebAddr,
				Title:    "anylan client",
				Token:    cfg.WebToken,
				Snapshot: func() any { return status.Snapshot() },
			}
			if cfg.Logs != nil {
				server.Logs = func() any { return cfg.Logs.Snapshot() }
			}
			if cfg.Capture != nil {
				server.Capture = func() any { return cfg.Capture.Snapshot() }
				server.CaptureEnable = captureEnableHandler(cfg.Capture)
				server.CaptureDisable = captureDisableHandler(cfg.Capture)
			}
			err := server.ListenAndServe(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("web UI error: %v", err)
			}
		}()
	}
	return runLoop(ctx, cfg, status)
}

func RunControl(ctx context.Context, webAddr, webToken string, logs *logmem.Recorder) error {
	if webAddr == "" {
		webAddr = DefaultWebAddr
	}
	controller := NewController(ctx)
	captures := capture.NewRecorder(capture.DefaultLimit)
	controller.capture = captures
	server := webui.Server{
		Addr:     webAddr,
		Title:    "anylan client",
		Token:    webToken,
		Snapshot: func() any { return controller.Snapshot() },
		Devices:  controller.Devices,
		Join:     controller.Join,
		Leave:    controller.Leave,
	}
	if logs != nil {
		server.Logs = func() any { return logs.Snapshot() }
	}
	server.Capture = func() any { return captures.Snapshot() }
	server.CaptureEnable = captureEnableHandler(captures)
	server.CaptureDisable = captureDisableHandler(captures)
	return server.ListenAndServe(ctx)
}

func captureEnableHandler(recorder *capture.Recorder) webui.APIHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		log.Printf("packet capture enabled")
		return recorder.Enable(), nil
	}
}

func captureDisableHandler(recorder *capture.Recorder) webui.APIHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		log.Printf("packet capture disabled")
		return recorder.Disable(), nil
	}
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
		Server:                   req.Server,
		Room:                     req.Room,
		DisplayName:              req.DisplayName,
		DeviceName:               req.DeviceName,
		InsecureSkipVerify:       req.InsecureSkipVerify,
		PrioritizeVirtualAdapter: req.PrioritizeVirtualAdapter,
	}
}

type Controller struct {
	ctx        context.Context
	mu         sync.Mutex
	status     *Status
	capture    *capture.Recorder
	configPath string
	cancel     context.CancelFunc
	done       chan struct{}
}

func NewController(ctx context.Context) *Controller {
	return NewControllerWithConfig(ctx, "")
}

func NewControllerWithConfig(ctx context.Context, configPath string) *Controller {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := loadClientConfig(configPath)
	if err != nil {
		log.Printf("load client config failed: %v", err)
		cfg = defaultControlConfig()
	}
	return &Controller{ctx: ctx, status: newIdleStatus(cfg), configPath: configPath}
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
	cfg.Capture = c.capture
	if err := normalizeConfig(&cfg); err != nil {
		return nil, webui.APIError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	log.Printf("join requested server=%s room=%q name=%q dev=%s", cfg.Server, cfg.Room, cfg.DisplayName, cfg.DeviceName)

	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		log.Printf("join rejected room=%q reason=%q", cfg.Room, "already joined")
		return nil, webui.APIError{Status: http.StatusConflict, Message: "already joined; leave the current room first"}
	}
	if err := saveClientConfig(c.configPath, cfg); err != nil {
		c.mu.Unlock()
		return nil, webui.APIError{Status: http.StatusInternalServerError, Message: fmt.Sprintf("save client config: %v", err)}
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
	snap := c.status.Snapshot()
	c.mu.Unlock()
	if cancel == nil {
		log.Printf("leave requested ignored reason=%q", "not joined")
		return c.status.Snapshot(), nil
	}
	log.Printf("leave requested server=%s room=%q peer=%s name=%q", snap.Server, snap.Room, snap.PeerID, snap.DisplayName)
	c.status.setState("leaving")
	cancel()
	return c.status.Snapshot(), nil
}

func runLoop(ctx context.Context, cfg Config, status *Status) error {

	backoff := time.Second
	for {
		status.setState("connecting")
		log.Printf("connecting server=%s room=%q name=%q dev=%s", cfg.Server, cfg.Room, cfg.DisplayName, cfg.DeviceName)
		err := runSession(ctx, cfg, status)
		if ctx.Err() != nil {
			logSessionEnd(status, ctx.Err(), "stopped")
			return ctx.Err()
		}
		if isFatal(err) {
			logSessionEnd(status, err, "stopped")
			status.endSession(err, "stopped")
			return err
		}
		logSessionEnd(status, err, "reconnecting")
		status.endSession(err, "reconnecting")
		log.Printf("reconnect scheduled server=%s room=%q reason=%q delay=%s", cfg.Server, cfg.Room, errorString(err), backoff)
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
	log.Printf("connected server=%s room=%q remote=%s", cfg.Server, cfg.Room, conn.RemoteAddr())

	var device *tap.Device
	var advertisedMAC string
	if tap.ShouldAdvertiseMAC() {
		device, err = tap.Open(cfg.DeviceName)
		if err != nil {
			return fatalf("open TAP device: %w", err)
		}
		defer device.Close()
		advertisedMAC, err = tap.AdvertiseMAC(device)
		if err != nil {
			return fatalf("read TAP MAC address: %w", err)
		}
		log.Printf("using TAP hardware MAC dev=%s mac=%s", device.Name(), advertisedMAC)
	}

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
		MAC:         advertisedMAC,
		Nonce:       nonce,
	}
	if err := protocol.WriteJSON(controlStream, protocol.TypeJoinRoom, join); err != nil {
		return err
	}
	log.Printf("join sent server=%s room=%q name=%q", cfg.Server, cfg.Room, cfg.DisplayName)

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
	log.Printf("join accepted room=%q peer=%s ip=%s mac=%s mtu=%d peers=%d", accept.Room, accept.PeerID, accept.IPv4, accept.MAC, accept.MTU, len(accept.Peers))

	if device == nil {
		device, err = tap.Open(cfg.DeviceName)
		if err != nil {
			return fatalf("open TAP device: %w", err)
		}
		defer device.Close()
	}

	if err := tap.Configure(ctx, device.Name(), accept.MAC, accept.CIDR, accept.MTU, cfg.PrioritizeVirtualAdapter); err != nil {
		return fatalf("configure TAP device: %w", err)
	}
	log.Printf("tap configured dev=%s layer=%s room=%q peer=%s cidr=%s mac=%s mtu=%d", device.Name(), device.Layer(), accept.Room, accept.PeerID, accept.CIDR, accept.MAC, accept.MTU)

	dataStream, err := conn.AcceptStream(ctx)
	if err != nil {
		return err
	}
	defer dataStream.Close()
	log.Printf("data stream ready room=%q peer=%s", accept.Room, accept.PeerID)

	errCh := make(chan error, 3)
	meta := capture.Metadata{Room: cfg.Room, PeerID: accept.PeerID, PeerName: cfg.DisplayName}
	switch device.Layer() {
	case tap.LayerIP:
		ownMAC, err := net.ParseMAC(accept.MAC)
		if err != nil {
			return fatalf("parse assigned MAC: %w", err)
		}
		ownIP := net.ParseIP(accept.IPv4).To4()
		if ownIP == nil {
			return fatalf("parse assigned IPv4: %s", accept.IPv4)
		}
		writer := &ethernetFrameWriter{w: dataStream}
		go readIPAsEthernet(device, writer, status, ownMAC, cfg.Capture, meta, errCh)
		go writeEthernetAsIP(device, dataStream, writer, status, ownMAC, ownIP, accept.CIDR, cfg.Capture, meta, errCh)
	default:
		go readTAP(device, dataStream, status, cfg.Capture, meta, errCh)
		go writeTAP(device, dataStream, status, cfg.Capture, meta, errCh)
	}
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

	server                   string
	room                     string
	displayName              string
	deviceName               string
	insecureSkipVerify       bool
	prioritizeVirtualAdapter bool
	state                    string
	lastError                string

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
	GeneratedAt              time.Time           `json:"generated_at"`
	Server                   string              `json:"server"`
	Room                     string              `json:"room"`
	DisplayName              string              `json:"display_name,omitempty"`
	DeviceName               string              `json:"device_name,omitempty"`
	InsecureSkipVerify       bool                `json:"insecure_skip_verify"`
	PrioritizeVirtualAdapter bool                `json:"prioritize_virtual_adapter"`
	State                    string              `json:"state"`
	LastError                string              `json:"last_error,omitempty"`
	PeerID                   string              `json:"peer_id,omitempty"`
	IPv4                     string              `json:"ipv4,omitempty"`
	CIDR                     string              `json:"cidr,omitempty"`
	MAC                      string              `json:"mac,omitempty"`
	MTU                      int                 `json:"mtu,omitempty"`
	JoinedAt                 time.Time           `json:"joined_at,omitempty"`
	RoomCreatedAt            time.Time           `json:"room_created_at,omitempty"`
	Peers                    []protocol.PeerInfo `json:"peers,omitempty"`
	RxBytes                  uint64              `json:"rx_bytes"`
	RxFrames                 uint64              `json:"rx_frames"`
	TxBytes                  uint64              `json:"tx_bytes"`
	TxFrames                 uint64              `json:"tx_frames"`
	Reconnects               uint64              `json:"reconnects"`
}

type sessionLogSnapshot struct {
	Server      string
	Room        string
	DisplayName string
	DeviceName  string
	State       string
	PeerID      string
	IPv4        string
	MAC         string
	JoinedAt    time.Time
	RxBytes     uint64
	RxFrames    uint64
	TxBytes     uint64
	TxFrames    uint64
	Reconnects  uint64
}

func newStatus(cfg Config) *Status {
	return &Status{
		server:                   cfg.Server,
		room:                     cfg.Room,
		displayName:              cfg.DisplayName,
		deviceName:               cfg.DeviceName,
		insecureSkipVerify:       cfg.InsecureSkipVerify,
		prioritizeVirtualAdapter: cfg.PrioritizeVirtualAdapter,
		state:                    "starting",
		updatedAt:                time.Now(),
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
	s.prioritizeVirtualAdapter = cfg.PrioritizeVirtualAdapter
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
	s.updatedAt = time.Now()
	log.Printf("peer list updated room=%q peer=%s peers=%d", s.room, s.peerID, len(pl.Peers))
}

func (s *Status) peerMACForIPv4(ip net.IP) net.HardwareAddr {
	if ip == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, peer := range s.peers {
		if peer.IPv4 == s.ipv4 || peer.IPv4 == "" || peer.MAC == "" {
			continue
		}
		peerIP := net.ParseIP(peer.IPv4).To4()
		if peerIP == nil || !peerIP.Equal(ip) {
			continue
		}
		mac, err := net.ParseMAC(peer.MAC)
		if err != nil {
			return nil
		}
		return mac
	}
	return nil
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

func (s *Status) sessionLogSnapshot() sessionLogSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionLogSnapshot{
		Server:      s.server,
		Room:        s.room,
		DisplayName: s.displayName,
		DeviceName:  s.deviceName,
		State:       s.state,
		PeerID:      s.peerID,
		IPv4:        s.ipv4,
		MAC:         s.mac,
		JoinedAt:    s.joinedAt,
		RxBytes:     s.rxBytes.Load(),
		RxFrames:    s.rxFrames.Load(),
		TxBytes:     s.txBytes.Load(),
		TxFrames:    s.txFrames.Load(),
		Reconnects:  s.reconnects.Load(),
	}
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

func logSessionEnd(status *Status, err error, nextState string) {
	snap := status.sessionLogSnapshot()
	duration := "0s"
	if !snap.JoinedAt.IsZero() {
		duration = time.Since(snap.JoinedAt).Round(time.Second).String()
	}
	log.Printf(
		"session ended server=%s room=%q peer=%s name=%q dev=%s next_state=%s reason=%q duration=%s rx_frames=%d rx_bytes=%d tx_frames=%d tx_bytes=%d reconnects=%d",
		snap.Server,
		snap.Room,
		snap.PeerID,
		snap.DisplayName,
		snap.DeviceName,
		nextState,
		errorString(err),
		duration,
		snap.RxFrames,
		snap.RxBytes,
		snap.TxFrames,
		snap.TxBytes,
		snap.Reconnects,
	)
}

func errorString(err error) string {
	if err == nil {
		return "closed"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	return err.Error()
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
		GeneratedAt:              time.Now(),
		Server:                   s.server,
		Room:                     s.room,
		DisplayName:              s.displayName,
		DeviceName:               s.deviceName,
		InsecureSkipVerify:       s.insecureSkipVerify,
		PrioritizeVirtualAdapter: s.prioritizeVirtualAdapter,
		State:                    s.state,
		LastError:                s.lastError,
		PeerID:                   s.peerID,
		IPv4:                     s.ipv4,
		CIDR:                     s.cidr,
		MAC:                      s.mac,
		MTU:                      s.mtu,
		JoinedAt:                 s.joinedAt,
		RoomCreatedAt:            s.roomCreatedAt,
		Peers:                    s.peers,
		RxBytes:                  s.rxBytes.Load(),
		RxFrames:                 s.rxFrames.Load(),
		TxBytes:                  s.txBytes.Load(),
		TxFrames:                 s.txFrames.Load(),
		Reconnects:               s.reconnects.Load(),
	}
}

func readTAP(device io.Reader, stream io.Writer, status *Status, captures *capture.Recorder, meta capture.Metadata, errCh chan<- error) {
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
		meta.Direction = "tx"
		captures.Record(meta, frame)
	}
}

func writeTAP(device io.Writer, stream io.Reader, status *Status, captures *capture.Recorder, meta capture.Metadata, errCh chan<- error) {
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
			meta.Direction = "rx"
			captures.Record(meta, payload)
		}
	}
}

type ethernetFrameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *ethernetFrameWriter) WriteFrame(frame []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return protocol.WriteMessage(w.w, protocol.TypeEthernetFrame, frame)
}

func readIPAsEthernet(device io.Reader, writer *ethernetFrameWriter, status *Status, ownMAC net.HardwareAddr, captures *capture.Recorder, meta capture.Metadata, errCh chan<- error) {
	buf := make([]byte, protocol.MaxFrameSize)
	for {
		n, err := device.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		if n == 0 || !isIPv4Packet(buf[:n]) {
			continue
		}
		dstMAC := status.peerMACForIPv4(ipv4Dst(buf[:n]))
		if dstMAC == nil {
			dstMAC = broadcastMAC()
		}
		frame := ethernetFrameFromIPv4(buf[:n], dstMAC, ownMAC)
		if err := writer.WriteFrame(frame); err != nil {
			errCh <- err
			return
		}
		status.recordTx(len(frame))
		meta.Direction = "tx"
		captures.Record(meta, frame)
	}
}

func writeEthernetAsIP(device io.Writer, stream io.Reader, writer *ethernetFrameWriter, status *Status, ownMAC net.HardwareAddr, ownIP net.IP, cidr string, captures *capture.Recorder, meta capture.Metadata, errCh chan<- error) {
	roomBroadcast := broadcastIPv4(cidr)
	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
		if err != nil {
			errCh <- err
			return
		}
		if typ != protocol.TypeEthernetFrame {
			continue
		}
		if isARPRequestForIPv4(payload, ownIP) {
			if err := writer.WriteFrame(arpReply(payload, ownMAC, ownIP)); err != nil {
				errCh <- err
				return
			}
			continue
		}
		if !isIPv4EthernetFrame(payload) {
			continue
		}
		packet := payload[14:]
		if !shouldDeliverIPv4ToTUN(packet, ownIP, roomBroadcast) {
			continue
		}
		if _, err := device.Write(packet); err != nil {
			errCh <- err
			return
		}
		status.recordRx(len(payload))
		meta.Direction = "rx"
		captures.Record(meta, payload)
	}
}

func ethernetFrameFromIPv4(packet []byte, dst, src net.HardwareAddr) []byte {
	frame := make([]byte, 14+len(packet))
	copy(frame[0:6], dst)
	copy(frame[6:12], src)
	frame[12] = 0x08
	frame[13] = 0x00
	copy(frame[14:], packet)
	return frame
}

func isIPv4Packet(packet []byte) bool {
	return len(packet) >= 20 && packet[0]>>4 == 4
}

func isIPv4EthernetFrame(frame []byte) bool {
	return len(frame) >= 34 && frame[12] == 0x08 && frame[13] == 0x00 && isIPv4Packet(frame[14:])
}

func ipv4Src(packet []byte) net.IP {
	return net.IP(packet[12:16])
}

func ipv4Dst(packet []byte) net.IP {
	return net.IP(packet[16:20])
}

func shouldDeliverIPv4ToTUN(packet []byte, ownIP, roomBroadcast net.IP) bool {
	dst := ipv4Dst(packet)
	if dst.Equal(ownIP) || dst.Equal(net.IPv4bcast) || dst.IsMulticast() {
		return true
	}
	return roomBroadcast != nil && dst.Equal(roomBroadcast)
}

func broadcastIPv4(cidr string) net.IP {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	ip := network.IP.To4()
	if ip == nil || len(network.Mask) != net.IPv4len {
		return nil
	}
	out := make(net.IP, net.IPv4len)
	for i := range out {
		out[i] = ip[i] | ^network.Mask[i]
	}
	return out
}

func broadcastMAC() net.HardwareAddr {
	return net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
}

func isARPRequestForIPv4(frame []byte, ownIP net.IP) bool {
	return len(frame) >= 42 &&
		frame[12] == 0x08 && frame[13] == 0x06 &&
		binary.BigEndian.Uint16(frame[14:16]) == 1 &&
		binary.BigEndian.Uint16(frame[16:18]) == 0x0800 &&
		frame[18] == 6 && frame[19] == 4 &&
		binary.BigEndian.Uint16(frame[20:22]) == 1 &&
		net.IP(frame[38:42]).Equal(ownIP)
}

func arpReply(request []byte, ownMAC net.HardwareAddr, ownIP net.IP) []byte {
	reply := make([]byte, 42)
	copy(reply[0:6], request[6:12])
	copy(reply[6:12], ownMAC)
	reply[12] = 0x08
	reply[13] = 0x06
	copy(reply[14:22], request[14:22])
	binary.BigEndian.PutUint16(reply[20:22], 2)
	copy(reply[22:28], ownMAC)
	copy(reply[28:32], ownIP.To4())
	copy(reply[32:38], request[22:28])
	copy(reply[38:42], request[28:32])
	return reply
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
