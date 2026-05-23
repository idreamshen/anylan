package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/quic-go/quic-go"
)

func TestServerRelaysFramesWithinRoomOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{InsecureDevCert: true}
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen packet failed: %v", err)
	}
	listener, err := server.Listen(ctx, udpConn)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = server.Serve(ctx, listener)
	}()

	a := joinTestClient(t, ctx, listener.Addr().String(), "same")
	defer a.close()
	b := joinTestClient(t, ctx, listener.Addr().String(), "same")
	defer b.close()
	c := joinTestClient(t, ctx, listener.Addr().String(), "same")
	defer c.close()
	other := joinTestClient(t, ctx, listener.Addr().String(), "other")
	defer other.close()

	aMAC := mac("02:00:00:00:00:0a")
	bMAC := mac("02:00:00:00:00:0b")
	broadcast := ethernetFrame(broadcastMAC(), aMAC)
	if err := protocol.WriteMessage(a.stream, protocol.TypeEthernetFrame, broadcast); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	got := readFrame(t, b.stream)
	if string(got) != string(broadcast) {
		t.Fatal("same-room peer received different frame")
	}
	got = readFrame(t, c.stream)
	if string(got) != string(broadcast) {
		t.Fatal("second same-room peer received different frame")
	}
	assertNoFrame(t, other.stream)

	unicast := ethernetFrame(aMAC, bMAC)
	if err := protocol.WriteMessage(b.stream, protocol.TypeEthernetFrame, unicast); err != nil {
		t.Fatalf("write unicast failed: %v", err)
	}
	got = readFrame(t, a.stream)
	if string(got) != string(unicast) {
		t.Fatal("known unicast recipient received different frame")
	}
	assertNoFrame(t, c.stream)
	assertNoFrame(t, other.stream)
}

func TestServerRejectsDifferentProtocolVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{InsecureDevCert: true}
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen packet failed: %v", err)
	}
	listener, err := server.Listen(ctx, udpConn)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()
	go func() {
		_ = server.Serve(ctx, listener)
	}()

	conn, err := quic.DialAddr(ctx, listener.Addr().String(), testTLSConfig(), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.CloseWithError(0, "")
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}
	defer stream.Close()

	join := protocol.JoinRoom{Version: protocol.Version + 1, Room: "room"}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinRoom, join); err != nil {
		t.Fatalf("write join failed: %v", err)
	}
	typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
	if err != nil {
		t.Fatalf("read reject failed: %v", err)
	}
	if typ != protocol.TypeJoinReject {
		t.Fatalf("response = %d, want reject", typ)
	}
	var reject protocol.JoinReject
	if err := json.Unmarshal(payload, &reject); err != nil {
		t.Fatalf("decode reject failed: %v", err)
	}
	if reject.Reason == "" {
		t.Fatal("reject reason is empty")
	}
}

type testClient struct {
	conn   quic.Connection
	stream quic.Stream
}

func (c testClient) close() {
	c.stream.Close()
	c.conn.CloseWithError(0, "")
}

func joinTestClient(t *testing.T, ctx context.Context, addr, room string) testClient {
	t.Helper()
	conn, err := quic.DialAddr(ctx, addr, testTLSConfig(), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}
	join := protocol.JoinRoom{Version: protocol.Version, Room: room}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinRoom, join); err != nil {
		t.Fatalf("write join failed: %v", err)
	}
	var accept protocol.JoinAccept
	if err := protocol.ReadJSON(stream, protocol.TypeJoinAccept, protocol.MaxControlSize, &accept); err != nil {
		t.Fatalf("read accept failed: %v", err)
	}
	return testClient{conn: conn, stream: stream}
}

func readFrame(t *testing.T, stream quic.Stream) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := stream.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	typ, payload, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
	if err != nil {
		t.Fatalf("read frame failed: %v", err)
	}
	if typ != protocol.TypeEthernetFrame {
		t.Fatalf("type = %d, want ethernet frame", typ)
	}
	return payload
}

func assertNoFrame(t *testing.T, stream quic.Stream) {
	t.Helper()
	if err := stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	_, _, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return
	}
	t.Fatalf("unexpected read result: %v", err)
}

func testTLSConfig() *tls.Config {
	return &tls.Config{
		NextProtos:         []string{alpn},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	}
}

func ethernetFrame(dst, src net.HardwareAddr) []byte {
	frame := make([]byte, 60)
	copy(frame[0:6], dst)
	copy(frame[6:12], src)
	frame[12] = 0x08
	frame[13] = 0x00
	return frame
}

func mac(s string) net.HardwareAddr {
	hw, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return hw
}

func broadcastMAC() net.HardwareAddr {
	return net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
}
