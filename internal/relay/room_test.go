package relay

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestManagerAssignsUniqueIPsAndIsolatesRooms(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))

	a1, err := manager.Join("a")
	if err != nil {
		t.Fatalf("join room a failed: %v", err)
	}
	a2, err := manager.Join("a")
	if err != nil {
		t.Fatalf("second join room a failed: %v", err)
	}
	b1, err := manager.Join("b")
	if err != nil {
		t.Fatalf("join room b failed: %v", err)
	}

	if a1.IP == a2.IP {
		t.Fatalf("same-room peers received duplicate IP %s", a1.IP)
	}
	if a1.Room == b1.Room {
		t.Fatal("different room names share a room")
	}
	if a1.Room.Prefix() == b1.Room.Prefix() {
		t.Fatalf("rooms share prefix %s", a1.Room.Prefix())
	}
}

func TestRoomRejectsWhenFull(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	room, err := manager.getOrCreateRoom("full")
	if err != nil {
		t.Fatalf("get room failed: %v", err)
	}
	room.nextHost = lastUsableHost + 1

	if _, err := room.AddPeer(); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("error = %v, want %v", err, ErrRoomFull)
	}
}

func TestForwardFloodsBroadcastAndLearnsSource(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")
	b, _ := manager.Join("room")
	c, _ := manager.Join("room")

	frame := ethernetFrame(broadcastMAC(), mac("02:00:00:00:00:0a"))
	targets, err := a.Room.Forward(a, frame)
	if err != nil {
		t.Fatalf("forward failed: %v", err)
	}
	assertTargets(t, targets, b, c)

	if got := a.Room.macToPeer[string(mac("02:00:00:00:00:0a"))]; got != a {
		t.Fatal("source MAC was not learned")
	}
}

func TestForwardUnicastsKnownDestination(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")
	b, _ := manager.Join("room")
	c, _ := manager.Join("room")

	bMAC := mac("02:00:00:00:00:0b")
	aMAC := mac("02:00:00:00:00:0a")
	if _, err := b.Room.Forward(b, ethernetFrame(broadcastMAC(), bMAC)); err != nil {
		t.Fatalf("learn b failed: %v", err)
	}

	targets, err := a.Room.Forward(a, ethernetFrame(bMAC, aMAC))
	if err != nil {
		t.Fatalf("forward failed: %v", err)
	}
	assertTargets(t, targets, b)
	if containsPeer(targets, c) {
		t.Fatal("known unicast was flooded")
	}
}

func TestRemovePeerRemovesMACTableEntries(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")
	learned := mac("02:00:00:00:00:0a")

	if _, err := a.Room.Forward(a, ethernetFrame(broadcastMAC(), learned)); err != nil {
		t.Fatalf("learn failed: %v", err)
	}
	a.Room.RemovePeer(a)

	if got := a.Room.macToPeer[string(learned)]; got != nil {
		t.Fatal("MAC entry survived peer removal")
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

func assertTargets(t *testing.T, got []*Peer, want ...*Peer) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d targets, want %d", len(got), len(want))
	}
	for _, peer := range want {
		if !containsPeer(got, peer) {
			t.Fatalf("missing target %s", peer.ID)
		}
	}
}

func containsPeer(peers []*Peer, want *Peer) bool {
	for _, peer := range peers {
		if peer == want {
			return true
		}
	}
	return false
}

func TestPeerEnqueueCopiesFrame(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	peer, _ := manager.Join("room")
	frame := []byte{1, 2, 3}
	peer.Enqueue(frame)
	frame[0] = 9

	got := <-peer.Frames
	if bytes.Equal(got, frame) {
		t.Fatal("queued frame aliases caller buffer")
	}
}
