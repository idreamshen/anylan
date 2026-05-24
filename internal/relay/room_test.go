package relay

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"sort"
	"testing"
	"time"
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

	frame := ethernetFrame(broadcastMAC(), a.MAC)
	targets, err := a.Room.Forward(a, frame)
	if err != nil {
		t.Fatalf("forward failed: %v", err)
	}
	assertTargets(t, targets, b, c)

	if got := a.Room.macToPeer[string(a.MAC)]; got != a {
		t.Fatal("source MAC was not learned")
	}
}

func TestForwardUnicastsKnownDestination(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")
	b, _ := manager.Join("room")
	c, _ := manager.Join("room")

	if _, err := b.Room.Forward(b, ethernetFrame(broadcastMAC(), b.MAC)); err != nil {
		t.Fatalf("learn b failed: %v", err)
	}

	targets, err := a.Room.Forward(a, ethernetFrame(b.MAC, a.MAC))
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
	if _, err := a.Room.Forward(a, ethernetFrame(broadcastMAC(), a.MAC)); err != nil {
		t.Fatalf("learn failed: %v", err)
	}
	a.Room.RemovePeer(a)

	if got := a.Room.macToPeer[string(a.MAC)]; got != nil {
		t.Fatal("MAC entry survived peer removal")
	}
}

func TestForwardRejectsSpoofedSourceMAC(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")

	frame := ethernetFrame(broadcastMAC(), mac("02:00:00:00:00:0a"))
	if _, err := a.Room.Forward(a, frame); !errors.Is(err, ErrSourceMACInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrSourceMACInvalid)
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
	if ok := peer.Enqueue(frame); !ok {
		t.Fatal("enqueue failed")
	}
	frame[0] = 9

	got := <-peer.Frames
	if bytes.Equal(got, frame) {
		t.Fatal("queued frame aliases caller buffer")
	}
}

func TestManagerSnapshotIncludesPeerMetadataAndCounters(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	peer, err := manager.Join("room", JoinOptions{DisplayName: "test peer"})
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}
	peer.RecordRxFrame(60)
	if ok := peer.Enqueue([]byte{1, 2, 3, 4}); !ok {
		t.Fatal("enqueue failed")
	}

	snapshot := manager.Snapshot()
	if snapshot.Pool != "10.240.0.0/12" {
		t.Fatalf("pool = %q", snapshot.Pool)
	}
	if len(snapshot.Rooms) != 1 {
		t.Fatalf("got %d rooms, want 1", len(snapshot.Rooms))
	}
	room := snapshot.Rooms[0]
	if room.Name != "room" {
		t.Fatalf("room name = %q", room.Name)
	}
	if len(room.Peers) != 1 {
		t.Fatalf("got %d peers, want 1", len(room.Peers))
	}
	got := room.Peers[0]
	if got.DisplayName != "test peer" {
		t.Fatalf("display name = %q", got.DisplayName)
	}
	if got.RxBytes != 60 || got.RxFrames != 1 {
		t.Fatalf("rx counters = %d/%d, want 60/1", got.RxBytes, got.RxFrames)
	}
	if got.TxBytes != 4 || got.TxFrames != 1 {
		t.Fatalf("tx counters = %d/%d, want 4/1", got.TxBytes, got.TxFrames)
	}
	peer.RecordDropFrame(7)
	got = manager.Snapshot().Rooms[0].Peers[0]
	if got.DropBytes != 7 || got.DropFrames != 1 {
		t.Fatalf("drop counters = %d/%d, want 7/1", got.DropBytes, got.DropFrames)
	}
}

func TestRemoveLastPeerDeletesRoomAndReusesPrefix(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/24"))
	peer, err := manager.Join("room-a")
	if err != nil {
		t.Fatalf("join room-a failed: %v", err)
	}
	prefix := peer.Room.Prefix()
	peer.Room.RemovePeer(peer)
	if _, ok := manager.Room("room-a"); ok {
		t.Fatal("empty room was not deleted")
	}

	peer, err = manager.Join("room-b")
	if err != nil {
		t.Fatalf("join room-b failed after room-a cleanup: %v", err)
	}
	if peer.Room.Prefix() != prefix {
		t.Fatalf("prefix = %s, want reused %s", peer.Room.Prefix(), prefix)
	}
}

func TestRoomReusesReleasedHost(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	a, _ := manager.Join("room")
	b, _ := manager.Join("room")
	released := a.IP
	a.Room.RemovePeer(a)

	c, err := manager.Join("room")
	if err != nil {
		t.Fatalf("join after release failed: %v", err)
	}
	if c.IP != released {
		t.Fatalf("reused IP = %s, want %s", c.IP, released)
	}
	if c.IP == b.IP {
		t.Fatalf("reused active peer IP %s", c.IP)
	}
}

func TestPeerRateLimit(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	peer, _ := manager.Join("room")
	if !peer.AllowFrame(maxPeerBytes) {
		t.Fatal("first frame should be allowed")
	}
	if peer.AllowFrame(1) {
		t.Fatal("frame over byte limit was allowed")
	}
}

func TestRoomSnapshotPeersSortedByConnectedAt(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))

	// Join 5 peers, then rewrite ConnectedAt to a known shuffled order so the
	// test does not rely on map iteration order or wall-clock spacing.
	const n = 5
	peers := make([]*Peer, 0, n)
	for i := 0; i < n; i++ {
		p, err := manager.Join("room")
		if err != nil {
			t.Fatalf("join %d failed: %v", i, err)
		}
		peers = append(peers, p)
	}

	base := time.Unix(1_700_000_000, 0)
	// Assign timestamps in a non-sorted order relative to peer creation order.
	offsets := []int{40, 10, 30, 0, 20}
	for i, off := range offsets {
		peers[i].ConnectedAt = base.Add(time.Duration(off) * time.Second)
	}

	rs := manager.Snapshot().Rooms[0]
	if len(rs.Peers) != n {
		t.Fatalf("snapshot peers = %d, want %d", len(rs.Peers), n)
	}
	for i := 1; i < len(rs.Peers); i++ {
		prev, cur := rs.Peers[i-1].ConnectedAt, rs.Peers[i].ConnectedAt
		if cur.Before(prev) {
			t.Fatalf("peers not sorted ascending by ConnectedAt: %v before %v at index %d", cur, prev, i)
		}
	}

	// Repeated calls must yield the same order.
	first := snapshotIDs(manager.Snapshot().Rooms[0])
	for i := 0; i < 20; i++ {
		got := snapshotIDs(manager.Snapshot().Rooms[0])
		if !equalStrings(first, got) {
			t.Fatalf("snapshot order not stable: first=%v got=%v", first, got)
		}
	}
}

func TestManagerSnapshotRoomsSortedByCreatedAt(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))

	// Create rooms in a name order that does NOT match the eventual createdAt order,
	// so a passing test cannot be explained by name-based or insertion-order sorting alone.
	names := []string{"charlie", "alpha", "delta", "bravo"}
	for _, name := range names {
		if _, err := manager.getOrCreateRoom(name); err != nil {
			t.Fatalf("create room %q failed: %v", name, err)
		}
	}

	base := time.Unix(1_700_000_000, 0)
	offsets := map[string]int{
		"charlie": 30,
		"alpha":   10,
		"delta":   40,
		"bravo":   20,
	}
	for name, off := range offsets {
		room, ok := manager.Room(name)
		if !ok {
			t.Fatalf("room %q missing", name)
		}
		room.createdAt = base.Add(time.Duration(off) * time.Second)
	}

	snap := manager.Snapshot()
	if len(snap.Rooms) != len(names) {
		t.Fatalf("got %d rooms, want %d", len(snap.Rooms), len(names))
	}
	wantOrder := []string{"alpha", "bravo", "charlie", "delta"}
	for i, want := range wantOrder {
		if snap.Rooms[i].Name != want {
			t.Fatalf("rooms[%d] = %q, want %q (full: %v)", i, snap.Rooms[i].Name, want, roomNames(snap))
		}
		if snap.Rooms[i].CreatedAt.IsZero() {
			t.Fatalf("rooms[%d].CreatedAt is zero", i)
		}
	}
	for i := 1; i < len(snap.Rooms); i++ {
		if snap.Rooms[i].CreatedAt.Before(snap.Rooms[i-1].CreatedAt) {
			t.Fatalf("rooms not sorted ascending by CreatedAt at index %d", i)
		}
	}

	// Stable across repeated calls.
	first := roomNames(snap)
	for i := 0; i < 20; i++ {
		if !equalStrings(first, roomNames(manager.Snapshot())) {
			t.Fatalf("rooms order not stable across snapshots")
		}
	}
}

func TestManagerSnapshotRoomsTiebreakByName(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))
	for _, name := range []string{"zulu", "alpha", "mike"} {
		if _, err := manager.getOrCreateRoom(name); err != nil {
			t.Fatalf("create room %q failed: %v", name, err)
		}
	}
	same := time.Unix(1_700_000_000, 0)
	for _, name := range []string{"zulu", "alpha", "mike"} {
		room, _ := manager.Room(name)
		room.createdAt = same
	}

	snap := manager.Snapshot()
	got := roomNames(snap)
	want := []string{"alpha", "mike", "zulu"}
	if !equalStrings(got, want) {
		t.Fatalf("tiebreak by name failed: got %v want %v", got, want)
	}
}

func roomNames(s ManagerSnapshot) []string {
	out := make([]string, 0, len(s.Rooms))
	for _, r := range s.Rooms {
		out = append(out, r.Name)
	}
	return out
}

func TestRoomSnapshotPeersTiebreakByID(t *testing.T) {
	manager := NewManager(netip.MustParsePrefix("10.240.0.0/12"))

	const n = 6
	peers := make([]*Peer, 0, n)
	for i := 0; i < n; i++ {
		p, err := manager.Join("room")
		if err != nil {
			t.Fatalf("join %d failed: %v", i, err)
		}
		peers = append(peers, p)
	}
	// Force identical ConnectedAt to exercise the tiebreak path.
	same := time.Unix(1_700_000_000, 0)
	for _, p := range peers {
		p.ConnectedAt = same
	}

	rs := manager.Snapshot().Rooms[0]
	ids := make([]string, 0, len(rs.Peers))
	for _, p := range rs.Peers {
		ids = append(ids, p.ID)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("tiebreak by ID failed; got %v", ids)
	}
}

func snapshotIDs(r RoomSnapshot) []string {
	out := make([]string, 0, len(r.Peers))
	for _, p := range r.Peers {
		out = append(out, p.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
