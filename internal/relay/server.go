package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

type Server struct {
	Addr            string
	Pool            netip.Prefix
	TLSCertFile     string
	TLSKeyFile      string
	InsecureDevCert bool
	MTU             int
}

func (s Server) ListenAndServe(ctx context.Context) error {
	if s.Addr == "" {
		s.Addr = ":4433"
	}
	if !s.Pool.IsValid() {
		s.Pool = netip.MustParsePrefix("10.240.0.0/12")
	}
	if s.MTU == 0 {
		s.MTU = protocol.DefaultMTU
	}

	tlsConfig, err := s.tlsConfig()
	if err != nil {
		return err
	}
	listener, err := quic.ListenAddr(s.Addr, tlsConfig, &quic.Config{
		MaxIdleTimeout: 60 * time.Second,
	})
	if err != nil {
		return err
	}
	defer listener.Close()

	return s.Serve(ctx, listener)
}

func (s Server) Serve(ctx context.Context, listener *quic.Listener) error {
	if !s.Pool.IsValid() {
		s.Pool = netip.MustParsePrefix("10.240.0.0/12")
	}
	if s.MTU == 0 {
		s.MTU = protocol.DefaultMTU
	}

	manager := NewManager(s.Pool)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go s.handleConnection(ctx, manager, conn)
	}
}

func (s Server) Listen(ctx context.Context, packetConn net.PacketConn) (*quic.Listener, error) {
	if !s.Pool.IsValid() {
		s.Pool = netip.MustParsePrefix("10.240.0.0/12")
	}
	if s.MTU == 0 {
		s.MTU = protocol.DefaultMTU
	}

	tlsConfig, err := s.tlsConfig()
	if err != nil {
		return nil, err
	}
	return quic.Listen(packetConn, tlsConfig, &quic.Config{
		MaxIdleTimeout: 60 * time.Second,
	})
}

func (s Server) handleConnection(ctx context.Context, manager *Manager, conn quic.Connection) {
	closeConn := true
	defer func() {
		if closeConn {
			conn.CloseWithError(0, "")
		}
	}()

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer stream.Close()

	var join protocol.JoinRoom
	if err := protocol.ReadJSON(stream, protocol.TypeJoinRoom, protocol.MaxControlSize, &join); err != nil {
		rejectAndCloseStream(stream, "invalid join request")
		closeConn = false
		return
	}
	join.Room = strings.TrimSpace(join.Room)
	if join.Version != protocol.Version {
		rejectAndCloseStream(stream, "unsupported protocol version")
		closeConn = false
		return
	}
	if join.Room == "" {
		rejectAndCloseStream(stream, "room is required")
		closeConn = false
		return
	}

	peer, err := manager.Join(join.Room)
	if err != nil {
		rejectAndCloseStream(stream, err.Error())
		closeConn = false
		return
	}
	defer peer.Room.RemovePeer(peer)

	accept := protocol.JoinAccept{
		Version: protocol.Version,
		Room:    join.Room,
		PeerID:  peer.ID,
		IPv4:    peer.IP.String(),
		CIDR:    netip.PrefixFrom(peer.IP, peer.Room.Prefix().Bits()).String(),
		MAC:     peer.MAC.String(),
		MTU:     s.MTU,
	}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinAccept, accept); err != nil {
		return
	}

	writeErr := make(chan error, 1)
	go func() {
		for frame := range peer.Frames {
			if err := protocol.WriteMessage(stream, protocol.TypeEthernetFrame, frame); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-writeErr:
			return
		default:
		}

		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return
			}
			return
		}
		switch typ {
		case protocol.TypeEthernetFrame:
			targets, err := peer.Room.Forward(peer, payload)
			if err != nil {
				continue
			}
			for _, target := range targets {
				target.Enqueue(payload)
			}
		case protocol.TypePing:
			if err := protocol.WriteMessage(stream, protocol.TypePong, payload); err != nil {
				return
			}
		}
	}
}

func reject(w io.Writer, reason string) error {
	return protocol.WriteJSON(w, protocol.TypeJoinReject, protocol.JoinReject{Reason: reason})
}

func rejectAndCloseStream(stream quic.Stream, reason string) {
	_ = reject(stream, reason)
	_ = stream.Close()
}

func (s Server) tlsConfig() (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	switch {
	case s.TLSCertFile != "" || s.TLSKeyFile != "":
		if s.TLSCertFile == "" || s.TLSKeyFile == "" {
			return nil, fmt.Errorf("both --tls-cert and --tls-key are required")
		}
		cert, err = tls.LoadX509KeyPair(s.TLSCertFile, s.TLSKeyFile)
	case s.InsecureDevCert:
		cert, err = devCertificate()
	default:
		return nil, fmt.Errorf("provide --tls-cert/--tls-key or use --insecure-dev-cert")
	}
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{alpn},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func devCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "anylan dev"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  nil,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func LoadPool(value string) (netip.Prefix, error) {
	if value == "" {
		return netip.MustParsePrefix("10.240.0.0/12"), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("pool must be IPv4")
	}
	if prefix.Bits() > 24 {
		return netip.Prefix{}, fmt.Errorf("pool must contain at least one /24")
	}
	return prefix.Masked(), nil
}
