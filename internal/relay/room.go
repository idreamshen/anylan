package relay

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	firstUsableHost = 2
	lastUsableHost  = 254

	frameRateWindow = time.Second
	maxPeerFrames   = 5000
	maxPeerBytes    = 20 * 1024 * 1024
)

var (
	ErrRoomFull         = errors.New("room is full")
	ErrRoomClosed       = errors.New("room is closed")
	ErrPoolFull         = errors.New("room pool is full")
	ErrMACInUse         = errors.New("mac address is already in use")
	ErrMACInvalid       = errors.New("invalid mac address")
	ErrInvalidFrame     = errors.New("invalid ethernet frame")
	ErrSourceMACInvalid = errors.New("ethernet source MAC does not match peer")
)

type Manager struct {
	mu       sync.Mutex
	pool     netip.Prefix
	rooms    map[string]*Room
	nextRoom uint32
	freeRoom []uint32
}

type Room struct {
	manager   *Manager
	index     uint32
	name      string
	prefix    netip.Prefix
	createdAt time.Time

	mu        sync.Mutex
	peers     map[string]*Peer
	macToPeer map[string]*Peer
	nextHost  uint8
	freeHosts []uint8
	closed    bool
}

type Peer struct {
	ID          string
	DisplayName string
	Room        *Room
	IP          netip.Addr
	MAC         net.HardwareAddr
	ConnectedAt time.Time
	Frames      chan []byte
	// Notify carries pre-serialised wire messages (header+payload) to be
	// forwarded to this peer's control stream (e.g. TypePeerList updates).
	Notify  chan []byte
	closing sync.Once

	rxBytes    atomic.Uint64
	rxFrames   atomic.Uint64
	txBytes    atomic.Uint64
	txFrames   atomic.Uint64
	dropBytes  atomic.Uint64
	dropFrames atomic.Uint64

	rateMu       sync.Mutex
	windowStart  time.Time
	windowFrames int
	windowBytes  int
}

type JoinOptions struct {
	DisplayName  string
	RequestedMAC net.HardwareAddr
}

type ManagerSnapshot struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Pool        string         `json:"pool"`
	Rooms       []RoomSnapshot `json:"rooms"`
}

type RoomSnapshot struct {
	Name      string         `json:"name"`
	Prefix    string         `json:"prefix"`
	CreatedAt time.Time      `json:"created_at"`
	Peers     []PeerSnapshot `json:"peers"`
}

type PeerSnapshot struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name,omitempty"`
	IP          string    `json:"ip"`
	MAC         string    `json:"mac"`
	ConnectedAt time.Time `json:"connected_at"`
	RxBytes     uint64    `json:"rx_bytes"`
	RxFrames    uint64    `json:"rx_frames"`
	TxBytes     uint64    `json:"tx_bytes"`
	TxFrames    uint64    `json:"tx_frames"`
	DropBytes   uint64    `json:"drop_bytes"`
	DropFrames  uint64    `json:"drop_frames"`
}

func NewManager(pool netip.Prefix) *Manager {
	return &Manager{
		pool:  pool,
		rooms: make(map[string]*Room),
	}
}

func (m *Manager) Join(roomName string, opts ...JoinOptions) (*Peer, error) {
	var opt JoinOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	for {
		room, err := m.getOrCreateRoom(roomName)
		if err != nil {
			return nil, err
		}
		peer, err := room.AddPeer(opt)
		if errors.Is(err, ErrRoomClosed) {
			continue
		}
		return peer, err
	}
}

func (m *Manager) Room(roomName string) (*Room, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	room, ok := m.rooms[roomName]
	return room, ok
}

func (m *Manager) Snapshot() ManagerSnapshot {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, room := range m.rooms {
		rooms = append(rooms, room)
	}
	pool := m.pool.String()
	m.mu.Unlock()

	// Stable order for UI/API consumers: oldest room first, tiebreak by name.
	sort.SliceStable(rooms, func(i, j int) bool {
		if rooms[i].createdAt.Equal(rooms[j].createdAt) {
			return rooms[i].name < rooms[j].name
		}
		return rooms[i].createdAt.Before(rooms[j].createdAt)
	})

	snapshot := ManagerSnapshot{
		GeneratedAt: time.Now(),
		Pool:        pool,
		Rooms:       make([]RoomSnapshot, 0, len(rooms)),
	}
	for _, room := range rooms {
		snapshot.Rooms = append(snapshot.Rooms, room.Snapshot())
	}
	return snapshot
}

func (m *Manager) getOrCreateRoom(roomName string) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if room, ok := m.rooms[roomName]; ok {
		return room, nil
	}

	roomIndex, err := m.allocateRoomIndexLocked()
	if err != nil {
		return nil, err
	}

	base, err := ipv4ToUint32(m.pool.Addr())
	if err != nil {
		return nil, err
	}
	roomAddr := uint32ToIPv4(base + (roomIndex << 8))

	room := &Room{
		manager:   m,
		index:     roomIndex,
		name:      roomName,
		prefix:    netip.PrefixFrom(roomAddr, 24),
		createdAt: time.Now(),
		peers:     make(map[string]*Peer),
		macToPeer: make(map[string]*Peer),
		nextHost:  firstUsableHost,
	}
	m.rooms[roomName] = room
	return room, nil
}

func (m *Manager) allocateRoomIndexLocked() (uint32, error) {
	if n := len(m.freeRoom); n > 0 {
		idx := m.freeRoom[n-1]
		m.freeRoom = m.freeRoom[:n-1]
		return idx, nil
	}

	maxRooms := uint32(1) << uint32(24-m.pool.Bits())
	if m.nextRoom >= maxRooms {
		return 0, ErrPoolFull
	}
	idx := m.nextRoom
	m.nextRoom++
	return idx, nil
}

func (m *Manager) deleteEmptyRoom(room *Room) {
	m.mu.Lock()
	defer m.mu.Unlock()

	room.mu.Lock()
	defer room.mu.Unlock()

	if m.rooms[room.name] != room || len(room.peers) != 0 {
		return
	}
	room.closed = true
	delete(m.rooms, room.name)
	m.freeRoom = append(m.freeRoom, room.index)
	sort.Slice(m.freeRoom, func(i, j int) bool { return m.freeRoom[i] > m.freeRoom[j] })
}

func (r *Room) Name() string {
	return r.name
}

func (r *Room) Prefix() netip.Prefix {
	return r.prefix
}

func (r *Room) PeerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

func (r *Room) AddPeer(opts ...JoinOptions) (*Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var opt JoinOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	if r.closed {
		return nil, ErrRoomClosed
	}

	peerID, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	mac := cloneMAC(opt.RequestedMAC)
	if mac != nil {
		if !validPeerMAC(mac) {
			return nil, ErrMACInvalid
		}
		if r.macToPeer[string(mac)] != nil {
			return nil, ErrMACInUse
		}
	} else {
		var err error
		mac, err = randomMAC()
		if err != nil {
			return nil, err
		}
	}

	host, err := r.allocateHostLocked()
	if err != nil {
		return nil, err
	}

	ipBase, err := ipv4ToUint32(r.prefix.Addr())
	if err != nil {
		return nil, err
	}
	ip := uint32ToIPv4(ipBase + uint32(host))

	peer := &Peer{
		ID:          peerID,
		DisplayName: opt.DisplayName,
		Room:        r,
		IP:          ip,
		MAC:         mac,
		ConnectedAt: time.Now(),
		Frames:      make(chan []byte, 128),
		Notify:      make(chan []byte, 16),
	}
	r.peers[peer.ID] = peer
	r.macToPeer[string(peer.MAC)] = peer
	return peer, nil
}

func (r *Room) allocateHostLocked() (uint8, error) {
	if n := len(r.freeHosts); n > 0 {
		host := r.freeHosts[0]
		copy(r.freeHosts, r.freeHosts[1:])
		r.freeHosts = r.freeHosts[:n-1]
		return host, nil
	}
	if r.nextHost > lastUsableHost {
		return 0, ErrRoomFull
	}
	host := r.nextHost
	r.nextHost++
	return host, nil
}

func (r *Room) Snapshot() RoomSnapshot {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	snapshot := RoomSnapshot{
		Name:      r.name,
		Prefix:    r.prefix.String(),
		CreatedAt: r.createdAt,
		Peers:     make([]PeerSnapshot, 0, len(peers)),
	}
	r.mu.Unlock()

	// Stable order for UI/API consumers: oldest connection first, tiebreak by ID.
	sort.SliceStable(peers, func(i, j int) bool {
		if peers[i].ConnectedAt.Equal(peers[j].ConnectedAt) {
			return peers[i].ID < peers[j].ID
		}
		return peers[i].ConnectedAt.Before(peers[j].ConnectedAt)
	})

	for _, peer := range peers {
		snapshot.Peers = append(snapshot.Peers, peer.Snapshot())
	}
	return snapshot
}

// BroadcastNotify sends msg to the Notify channel of every peer currently in
// the room, skipping exclude (may be nil).  The send is non-blocking; peers
// whose channel is full silently drop the message.
func (r *Room) BroadcastNotify(msg []byte, exclude *Peer) {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		if p != exclude {
			peers = append(peers, p)
		}
	}
	r.mu.Unlock()

	for _, p := range peers {
		select {
		case p.Notify <- msg:
		default:
		}
	}
}

func (r *Room) RemovePeer(peer *Peer) {
	r.mu.Lock()
	if existing := r.peers[peer.ID]; existing != peer {
		r.mu.Unlock()
		return
	}
	delete(r.peers, peer.ID)
	r.releaseHostLocked(peer.IP)
	for mac, owner := range r.macToPeer {
		if owner == peer {
			delete(r.macToPeer, mac)
		}
	}
	empty := len(r.peers) == 0
	manager := r.manager
	r.mu.Unlock()

	peer.closeFrames()
	if empty && manager != nil {
		manager.deleteEmptyRoom(r)
	}
}

func (r *Room) releaseHostLocked(ip netip.Addr) {
	if !ip.Is4() {
		return
	}
	host := ip.As4()[3]
	if host < firstUsableHost || host > lastUsableHost {
		return
	}
	for _, free := range r.freeHosts {
		if free == host {
			return
		}
	}
	r.freeHosts = append(r.freeHosts, host)
	sort.Slice(r.freeHosts, func(i, j int) bool { return r.freeHosts[i] < r.freeHosts[j] })
}

func (r *Room) Forward(from *Peer, frame []byte) ([]*Peer, error) {
	if len(frame) < 14 {
		return nil, ErrInvalidFrame
	}

	dst := frame[0:6]
	src := frame[6:12]
	dstKey := string(dst)
	if !bytes.Equal(src, from.MAC) {
		return nil, ErrSourceMACInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if current := r.peers[from.ID]; current != from {
		return nil, nil
	}
	r.macToPeer[string(from.MAC)] = from

	if isBroadcast(dst) || isMulticast(dst) {
		return r.otherPeersLocked(from), nil
	}

	if target := r.macToPeer[dstKey]; target != nil && target != from {
		if r.peers[target.ID] == target {
			return []*Peer{target}, nil
		}
	}

	return r.otherPeersLocked(from), nil
}

func (p *Peer) Enqueue(frame []byte) bool {
	copied := append([]byte(nil), frame...)
	select {
	case p.Frames <- copied:
		p.RecordTxFrame(len(copied))
		return true
	default:
		p.RecordDropFrame(len(copied))
		return false
	}
}

func (p *Peer) AllowFrame(size int) bool {
	now := time.Now()
	p.rateMu.Lock()
	defer p.rateMu.Unlock()

	if p.windowStart.IsZero() || now.Sub(p.windowStart) >= frameRateWindow {
		p.windowStart = now
		p.windowFrames = 0
		p.windowBytes = 0
	}
	if p.windowFrames+1 > maxPeerFrames || p.windowBytes+size > maxPeerBytes {
		return false
	}
	p.windowFrames++
	p.windowBytes += size
	return true
}

func (p *Peer) RecordRxFrame(size int) {
	p.rxFrames.Add(1)
	p.rxBytes.Add(uint64(size))
}

func (p *Peer) RecordTxFrame(size int) {
	p.txFrames.Add(1)
	p.txBytes.Add(uint64(size))
}

func (p *Peer) RecordDropFrame(size int) {
	p.dropFrames.Add(1)
	p.dropBytes.Add(uint64(size))
}

func (p *Peer) Snapshot() PeerSnapshot {
	return PeerSnapshot{
		ID:          p.ID,
		DisplayName: p.DisplayName,
		IP:          p.IP.String(),
		MAC:         p.MAC.String(),
		ConnectedAt: p.ConnectedAt,
		RxBytes:     p.rxBytes.Load(),
		RxFrames:    p.rxFrames.Load(),
		TxBytes:     p.txBytes.Load(),
		TxFrames:    p.txFrames.Load(),
		DropBytes:   p.dropBytes.Load(),
		DropFrames:  p.dropFrames.Load(),
	}
}

func (p *Peer) closeFrames() {
	p.closing.Do(func() {
		close(p.Frames)
	})
}

func (r *Room) otherPeersLocked(from *Peer) []*Peer {
	peers := make([]*Peer, 0, len(r.peers)-1)
	for _, peer := range r.peers {
		if peer != from {
			peers = append(peers, peer)
		}
	}
	return peers
}

func isBroadcast(mac []byte) bool {
	for _, b := range mac {
		if b != 0xff {
			return false
		}
	}
	return true
}

func isMulticast(mac []byte) bool {
	return len(mac) > 0 && mac[0]&1 == 1
}

func validPeerMAC(mac net.HardwareAddr) bool {
	if len(mac) != 6 || isBroadcast(mac) || isMulticast(mac) {
		return false
	}
	for _, b := range mac {
		if b != 0 {
			return true
		}
	}
	return false
}

func cloneMAC(mac net.HardwareAddr) net.HardwareAddr {
	if mac == nil {
		return nil
	}
	return append(net.HardwareAddr(nil), mac...)
}

func randomMAC() (net.HardwareAddr, error) {
	mac := make([]byte, 6)
	if _, err := rand.Read(mac); err != nil {
		return nil, err
	}
	mac[0] = (mac[0] | 0x02) & 0xfe
	return net.HardwareAddr(mac), nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func ipv4ToUint32(addr netip.Addr) (uint32, error) {
	if !addr.Is4() {
		return 0, fmt.Errorf("not an IPv4 address: %s", addr)
	}
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:]), nil
}

func uint32ToIPv4(v uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], v)
	return netip.AddrFrom4(bytes)
}
