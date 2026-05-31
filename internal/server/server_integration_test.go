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

	aMAC := mac(a.accept.MAC)
	bMAC := mac(b.accept.MAC)
	broadcast := ethernetFrame(broadcastMAC(), aMAC)
	if err := protocol.WriteMessage(a.data, protocol.TypeEthernetFrame, broadcast); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	got := readFrame(t, b.data)
	if string(got) != string(broadcast) {
		t.Fatal("same-room peer received different frame")
	}
	got = readFrame(t, c.data)
	if string(got) != string(broadcast) {
		t.Fatal("second same-room peer received different frame")
	}
	assertNoFrame(t, other.data)

	unicast := ethernetFrame(aMAC, bMAC)
	if err := protocol.WriteMessage(b.data, protocol.TypeEthernetFrame, unicast); err != nil {
		t.Fatalf("write unicast failed: %v", err)
	}
	got = readFrame(t, a.data)
	if string(got) != string(unicast) {
		t.Fatal("known unicast recipient received different frame")
	}
	assertNoFrame(t, c.data)
	assertNoFrame(t, other.data)
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

func TestServerUsesRequestedMAC(t *testing.T) {
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

	requested := "02:00:00:00:00:0a"
	client := joinTestClientWithMAC(t, ctx, listener.Addr().String(), "room", requested)
	defer client.close()
	if client.accept.MAC != requested {
		t.Fatalf("accepted MAC = %s, want %s", client.accept.MAC, requested)
	}
}

func TestServerForwardsPeerLatencyMessages(t *testing.T) {
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

	ping := protocol.PeerPing{ID: "probe-1", ToPeerID: b.accept.PeerID, SentAtUnixNano: 123}
	if err := protocol.WriteJSON(a.stream, protocol.TypePing, ping); err != nil {
		t.Fatalf("write peer ping failed: %v", err)
	}
	gotPing := readPeerPing(t, b.stream)
	if gotPing.ID != ping.ID || gotPing.FromPeerID != a.accept.PeerID || gotPing.ToPeerID != b.accept.PeerID {
		t.Fatalf("forwarded ping = %#v", gotPing)
	}

	pong := protocol.PeerPong{ID: gotPing.ID, ToPeerID: gotPing.FromPeerID, SentAtUnixNano: gotPing.SentAtUnixNano}
	if err := protocol.WriteJSON(b.stream, protocol.TypePong, pong); err != nil {
		t.Fatalf("write peer pong failed: %v", err)
	}
	gotPong := readPeerPong(t, a.stream)
	if gotPong.ID != pong.ID || gotPong.FromPeerID != b.accept.PeerID || gotPong.ToPeerID != a.accept.PeerID {
		t.Fatalf("forwarded pong = %#v", gotPong)
	}
}

type testClient struct {
	conn   quic.Connection
	stream quic.Stream
	data   quic.Stream
	accept protocol.JoinAccept
}

func (c testClient) close() {
	c.data.Close()
	c.stream.Close()
	c.conn.CloseWithError(0, "")
}

func joinTestClient(t *testing.T, ctx context.Context, addr, room string) testClient {
	t.Helper()
	return joinTestClientWithMAC(t, ctx, addr, room, "")
}

func joinTestClientWithMAC(t *testing.T, ctx context.Context, addr, room, requestedMAC string) testClient {
	t.Helper()
	conn, err := quic.DialAddr(ctx, addr, testTLSConfig(), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}
	join := protocol.JoinRoom{Version: protocol.Version, Room: room, MAC: requestedMAC, Features: []string{protocol.FeaturePeerLatency}}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinRoom, join); err != nil {
		t.Fatalf("write join failed: %v", err)
	}
	var accept protocol.JoinAccept
	if err := protocol.ReadJSON(stream, protocol.TypeJoinAccept, protocol.MaxControlSize, &accept); err != nil {
		t.Fatalf("read accept failed: %v", err)
	}
	data, err := conn.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("open data stream failed: %v", err)
	}
	return testClient{conn: conn, stream: stream, data: data, accept: accept}
}

func readPeerPing(t *testing.T, stream quic.Stream) protocol.PeerPing {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := stream.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})
	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
		if err != nil {
			t.Fatalf("read peer ping failed: %v", err)
		}
		if typ == protocol.TypePeerList {
			continue
		}
		if typ != protocol.TypePing {
			t.Fatalf("type = %d, want peer ping", typ)
		}
		var ping protocol.PeerPing
		if err := json.Unmarshal(payload, &ping); err != nil {
			t.Fatalf("decode peer ping failed: %v", err)
		}
		if ping.ID == "" {
			continue
		}
		return ping
	}
}

func readPeerPong(t *testing.T, stream quic.Stream) protocol.PeerPong {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := stream.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})
	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
		if err != nil {
			t.Fatalf("read peer pong failed: %v", err)
		}
		if typ == protocol.TypePeerList {
			continue
		}
		if typ != protocol.TypePong {
			t.Fatalf("type = %d, want peer pong", typ)
		}
		var pong protocol.PeerPong
		if err := json.Unmarshal(payload, &pong); err != nil {
			t.Fatalf("decode peer pong failed: %v", err)
		}
		return pong
	}
}

func readFrame(t *testing.T, stream quic.Stream) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := stream.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set deadline failed: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	for {
		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
		if err != nil {
			t.Fatalf("read frame failed: %v", err)
		}
		if typ == protocol.TypePeerList || typ == protocol.TypePing {
			// skip control/ready messages; keep waiting for an ethernet frame
			continue
		}
		if typ != protocol.TypeEthernetFrame {
			t.Fatalf("type = %d, want ethernet frame", typ)
		}
		return payload
	}
}

func assertNoFrame(t *testing.T, stream quic.Stream) {
	t.Helper()

	for {
		if err := stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatalf("set deadline failed: %v", err)
		}
		typ, _, err := protocol.ReadMessage(stream, protocol.MaxControlSize)
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			stream.SetReadDeadline(time.Time{})
			return
		}
		if err != nil {
			stream.SetReadDeadline(time.Time{})
			t.Fatalf("unexpected read result: %v", err)
		}
		if typ == protocol.TypePeerList || typ == protocol.TypePing {
			// control/ready messages are expected; not an ethernet frame, keep waiting
			continue
		}
		stream.SetReadDeadline(time.Time{})
		t.Fatalf("unexpected ethernet frame received")
	}
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
