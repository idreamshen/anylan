package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/netip"
	"time"

	"github.com/idreamshen/anylan/internal/control"
	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/relay"
	"github.com/idreamshen/anylan/internal/webui"
	"github.com/quic-go/quic-go"
)

const alpn = "anylan-mvp"

type Server struct {
	Addr            string
	Pool            netip.Prefix
	RoomKey         string
	TLSCertFile     string
	TLSKeyFile      string
	InsecureDevCert bool
	MTU             int
	WebAddr         string
	WebToken        string
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
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
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

	manager := relay.NewManager(s.Pool)
	handler := control.Handler{
		Manager: manager,
		MTU:     s.MTU,
		RoomKey: s.RoomKey,
	}
	if s.WebAddr != "" {
		go func() {
			err := webui.Server{
				Addr:     s.WebAddr,
				Title:    "anylan server",
				Token:    s.WebToken,
				Snapshot: func() any { return manager.Snapshot() },
			}.ListenAndServe(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("web UI error: %v", err)
			}
		}()
	}
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go handler.HandleConnection(ctx, conn)
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
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
	})
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
