package relay

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	firstUsableHost = 2
	lastUsableHost  = 254
)

var (
	ErrRoomFull     = errors.New("room is full")
	ErrPoolFull     = errors.New("room pool is full")
	ErrInvalidFrame = errors.New("invalid ethernet frame")
)

type Manager struct {
	mu       sync.Mutex
	pool     netip.Prefix
	rooms    map[string]*Room
	nextRoom uint32
}

type Room struct {
	name   string
	prefix netip.Prefix

	mu        sync.Mutex
	peers     map[string]*Peer
	macToPeer map[string]*Peer
	nextHost  uint8
}

type Peer struct {
	ID          string
	DisplayName string
	Room        *Room
	IP          netip.Addr
	MAC         net.HardwareAddr
	ConnectedAt time.Time
	Frames      chan []byte
	closing     sync.Once

	rxBytes  atomic.Uint64
	rxFrames atomic.Uint64
	txBytes  atomic.Uint64
	txFrames atomic.Uint64
}

type JoinOptions struct {
	DisplayName string
}

type ManagerSnapshot struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Pool        string         `json:"pool"`
	Rooms       []RoomSnapshot `json:"rooms"`
}

type RoomSnapshot struct {
	Name   string         `json:"name"`
	Prefix string         `json:"prefix"`
	Peers  []PeerSnapshot `json:"peers"`
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
}

func NewManager(pool netip.Prefix) *Manager {
	return &Manager{
		pool:  pool,
		rooms: make(map[string]*Room),
	}
}

func (m *Manager) Join(roomName string, opts ...JoinOptions) (*Peer, error) {
	room, err := m.getOrCreateRoom(roomName)
	if err != nil {
		return nil, err
	}
	var opt JoinOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	return room.AddPeer(opt)
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

	maxRooms := uint32(1) << uint32(24-m.pool.Bits())
	if m.nextRoom >= maxRooms {
		return nil, ErrPoolFull
	}

	base, err := ipv4ToUint32(m.pool.Addr())
	if err != nil {
		return nil, err
	}
	roomAddr := uint32ToIPv4(base + (m.nextRoom << 8))
	m.nextRoom++

	room := &Room{
		name:      roomName,
		prefix:    netip.PrefixFrom(roomAddr, 24),
		peers:     make(map[string]*Peer),
		macToPeer: make(map[string]*Peer),
		nextHost:  firstUsableHost,
	}
	m.rooms[roomName] = room
	return room, nil
}

func (r *Room) Name() string {
	return r.name
}

func (r *Room) Prefix() netip.Prefix {
	return r.prefix
}

func (r *Room) AddPeer(opts ...JoinOptions) (*Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.nextHost > lastUsableHost {
		return nil, ErrRoomFull
	}

	peerID, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	mac, err := randomMAC()
	if err != nil {
		return nil, err
	}

	ipBase, err := ipv4ToUint32(r.prefix.Addr())
	if err != nil {
		return nil, err
	}
	ip := uint32ToIPv4(ipBase + uint32(r.nextHost))
	r.nextHost++

	var opt JoinOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	peer := &Peer{
		ID:          peerID,
		DisplayName: opt.DisplayName,
		Room:        r,
		IP:          ip,
		MAC:         mac,
		ConnectedAt: time.Now(),
		Frames:      make(chan []byte, 128),
	}
	r.peers[peer.ID] = peer
	return peer, nil
}

func (r *Room) Snapshot() RoomSnapshot {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	snapshot := RoomSnapshot{
		Name:   r.name,
		Prefix: r.prefix.String(),
		Peers:  make([]PeerSnapshot, 0, len(peers)),
	}
	r.mu.Unlock()

	for _, peer := range peers {
		snapshot.Peers = append(snapshot.Peers, peer.Snapshot())
	}
	return snapshot
}

func (r *Room) RemovePeer(peer *Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.peers[peer.ID]; existing != peer {
		return
	}
	delete(r.peers, peer.ID)
	for mac, owner := range r.macToPeer {
		if owner == peer {
			delete(r.macToPeer, mac)
		}
	}
	peer.closeFrames()
}

func (r *Room) Forward(from *Peer, frame []byte) ([]*Peer, error) {
	if len(frame) < 14 {
		return nil, ErrInvalidFrame
	}

	dst := frame[0:6]
	src := frame[6:12]
	srcKey := string(src)
	dstKey := string(dst)

	r.mu.Lock()
	defer r.mu.Unlock()

	if current := r.peers[from.ID]; current != from {
		return nil, nil
	}
	r.macToPeer[srcKey] = from

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
		return false
	}
}

func (p *Peer) RecordRxFrame(size int) {
	p.rxFrames.Add(1)
	p.rxBytes.Add(uint64(size))
}

func (p *Peer) RecordTxFrame(size int) {
	p.txFrames.Add(1)
	p.txBytes.Add(uint64(size))
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
