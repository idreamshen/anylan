package relay

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
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
	ID      string
	Room    *Room
	IP      netip.Addr
	MAC     net.HardwareAddr
	Frames  chan []byte
	closing sync.Once
}

func NewManager(pool netip.Prefix) *Manager {
	return &Manager{
		pool:  pool,
		rooms: make(map[string]*Room),
	}
}

func (m *Manager) Join(roomName string) (*Peer, error) {
	room, err := m.getOrCreateRoom(roomName)
	if err != nil {
		return nil, err
	}
	return room.AddPeer()
}

func (m *Manager) Room(roomName string) (*Room, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	room, ok := m.rooms[roomName]
	return room, ok
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

func (r *Room) AddPeer() (*Peer, error) {
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

	peer := &Peer{
		ID:     peerID,
		Room:   r,
		IP:     ip,
		MAC:    mac,
		Frames: make(chan []byte, 128),
	}
	r.peers[peer.ID] = peer
	return peer, nil
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

func (p *Peer) Enqueue(frame []byte) {
	copied := append([]byte(nil), frame...)
	select {
	case p.Frames <- copied:
	default:
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
