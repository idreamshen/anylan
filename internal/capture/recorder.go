package capture

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultLimit = 100

type Recorder struct {
	enabled atomic.Bool
	mu      sync.Mutex
	limit   int
	nextSeq uint64
	events  []Event
}

type Event struct {
	Seq       uint64    `json:"seq"`
	Time      time.Time `json:"time"`
	Direction string    `json:"direction"`
	Room      string    `json:"room,omitempty"`
	PeerID    string    `json:"peer_id,omitempty"`
	PeerName  string    `json:"peer_name,omitempty"`
	Size      int       `json:"size"`
	Valid     bool      `json:"valid"`
	EtherType string    `json:"ether_type"`
	TypeName  string    `json:"type_name"`
	Summary   string    `json:"summary"`
	SrcMAC    string    `json:"src_mac,omitempty"`
	DstMAC    string    `json:"dst_mac,omitempty"`
	SrcIP     string    `json:"src_ip,omitempty"`
	DstIP     string    `json:"dst_ip,omitempty"`
	IPProto   string    `json:"ip_protocol,omitempty"`
	SrcPort   int       `json:"src_port,omitempty"`
	DstPort   int       `json:"dst_port,omitempty"`
	VLAN      bool      `json:"vlan,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Enabled     bool      `json:"enabled"`
	Limit       int       `json:"limit"`
	Count       int       `json:"count"`
	Events      []Event   `json:"events"`
}

type Metadata struct {
	Direction string
	Room      string
	PeerID    string
	PeerName  string
}

func NewRecorder(limit int) *Recorder {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Recorder{limit: limit}
}

func (r *Recorder) Enable() Snapshot {
	if r == nil {
		return Snapshot{GeneratedAt: time.Now(), Limit: DefaultLimit}
	}
	r.enabled.Store(true)
	return r.Snapshot()
}

func (r *Recorder) Disable() Snapshot {
	if r == nil {
		return Snapshot{GeneratedAt: time.Now(), Limit: DefaultLimit}
	}
	r.enabled.Store(false)
	return r.Snapshot()
}

func (r *Recorder) Enabled() bool {
	return r != nil && r.enabled.Load()
}

func (r *Recorder) Record(meta Metadata, frame []byte) {
	if r == nil || !r.enabled.Load() {
		return
	}
	event := parseFrame(frame)
	event.Time = time.Now()
	event.Direction = meta.Direction
	event.Room = meta.Room
	event.PeerID = meta.PeerID
	event.PeerName = meta.PeerName

	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq++
	event.Seq = r.nextSeq
	if len(r.events) >= r.limit {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = event
		return
	}
	r.events = append(r.events, event)
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{GeneratedAt: time.Now(), Limit: DefaultLimit}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]Event, len(r.events))
	copy(events, r.events)
	return Snapshot{
		GeneratedAt: time.Now(),
		Enabled:     r.enabled.Load(),
		Limit:       r.limit,
		Count:       len(events),
		Events:      events,
	}
}

func parseFrame(frame []byte) Event {
	event := Event{Size: len(frame), EtherType: "unknown", TypeName: "Unknown", Summary: "Invalid frame"}
	if len(frame) < 14 {
		event.Reason = "frame too short"
		return event
	}
	event.Valid = true
	event.DstMAC = net.HardwareAddr(frame[0:6]).String()
	event.SrcMAC = net.HardwareAddr(frame[6:12]).String()
	etherType := binary.BigEndian.Uint16(frame[12:14])
	if etherType == 0x8100 || etherType == 0x88a8 {
		event.VLAN = true
		if len(frame) < 18 {
			event.Valid = false
			event.Reason = "vlan frame too short"
			return event
		}
		etherType = binary.BigEndian.Uint16(frame[16:18])
	}
	event.EtherType = fmt.Sprintf("0x%04x", etherType)
	event.TypeName = etherTypeName(etherType)
	event.Summary = event.TypeName
	if event.TypeName == "Unknown" {
		event.Summary = fmt.Sprintf("Unknown EtherType %s", event.EtherType)
	}
	payload := frame[14:]
	if event.VLAN {
		payload = frame[18:]
	}
	parsePayload(&event, etherType, payload)
	return event
}

func parsePayload(event *Event, etherType uint16, payload []byte) {
	switch etherType {
	case 0x0800:
		parseIPv4(event, payload)
	case 0x0806:
		parseARP(event, payload)
	case 0x86dd:
		parseIPv6(event, payload)
	}
}

func parseARP(event *Event, payload []byte) {
	if len(payload) < 28 {
		event.Summary = "ARP truncated"
		return
	}
	hardwareType := binary.BigEndian.Uint16(payload[0:2])
	protocolType := binary.BigEndian.Uint16(payload[2:4])
	hardwareSize := payload[4]
	protocolSize := payload[5]
	opcode := binary.BigEndian.Uint16(payload[6:8])
	if hardwareType != 1 || protocolType != 0x0800 || hardwareSize != 6 || protocolSize != 4 {
		event.Summary = fmt.Sprintf("ARP opcode %d", opcode)
		return
	}
	senderMAC := net.HardwareAddr(payload[8:14]).String()
	senderIP := net.IP(payload[14:18]).String()
	targetIP := net.IP(payload[24:28]).String()
	event.SrcIP = senderIP
	event.DstIP = targetIP
	switch opcode {
	case 1:
		event.Summary = fmt.Sprintf("ARP request who-has %s tell %s", targetIP, senderIP)
	case 2:
		event.Summary = fmt.Sprintf("ARP reply %s is-at %s", senderIP, senderMAC)
	default:
		event.Summary = fmt.Sprintf("ARP opcode %d", opcode)
	}
}

func parseIPv4(event *Event, payload []byte) {
	if len(payload) < 20 {
		event.Summary = "IPv4 truncated"
		return
	}
	version := payload[0] >> 4
	ihl := int(payload[0]&0x0f) * 4
	if version != 4 || ihl < 20 || len(payload) < ihl {
		event.Summary = "IPv4 invalid header"
		return
	}
	proto := payload[9]
	event.SrcIP = net.IP(payload[12:16]).String()
	event.DstIP = net.IP(payload[16:20]).String()
	event.IPProto = ipProtocolName(proto)
	transport := payload[ihl:]
	switch proto {
	case 1:
		parseICMPv4(event, transport)
	case 6:
		parsePorts(event, transport, "IPv4 TCP")
	case 17:
		parsePorts(event, transport, "IPv4 UDP")
	default:
		event.Summary = fmt.Sprintf("IPv4 protocol %d %s -> %s", proto, event.SrcIP, event.DstIP)
	}
}

func parseICMPv4(event *Event, payload []byte) {
	if len(payload) < 2 {
		event.Summary = fmt.Sprintf("IPv4 ICMP truncated %s -> %s", event.SrcIP, event.DstIP)
		return
	}
	switch payload[0] {
	case 0:
		event.Summary = fmt.Sprintf("IPv4 ICMP echo reply (ping) %s -> %s", event.SrcIP, event.DstIP)
	case 8:
		event.Summary = fmt.Sprintf("IPv4 ICMP echo request (ping) %s -> %s", event.SrcIP, event.DstIP)
	default:
		event.Summary = fmt.Sprintf("IPv4 ICMP type %d code %d %s -> %s", payload[0], payload[1], event.SrcIP, event.DstIP)
	}
}

func parseIPv6(event *Event, payload []byte) {
	if len(payload) < 40 {
		event.Summary = "IPv6 truncated"
		return
	}
	if payload[0]>>4 != 6 {
		event.Summary = "IPv6 invalid header"
		return
	}
	nextHeader := payload[6]
	event.SrcIP = net.IP(payload[8:24]).String()
	event.DstIP = net.IP(payload[24:40]).String()
	event.IPProto = ipProtocolName(nextHeader)
	transport := payload[40:]
	switch nextHeader {
	case 58:
		parseICMPv6(event, transport)
	case 6:
		parsePorts(event, transport, "IPv6 TCP")
	case 17:
		parsePorts(event, transport, "IPv6 UDP")
	default:
		event.Summary = fmt.Sprintf("IPv6 next-header %d %s -> %s", nextHeader, event.SrcIP, event.DstIP)
	}
}

func parseICMPv6(event *Event, payload []byte) {
	if len(payload) < 2 {
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 truncated %s -> %s", event.SrcIP, event.DstIP)
		return
	}
	switch payload[0] {
	case 128:
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 echo request (ping) %s -> %s", event.SrcIP, event.DstIP)
	case 129:
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 echo reply (ping) %s -> %s", event.SrcIP, event.DstIP)
	case 135:
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 neighbor solicitation %s -> %s", event.SrcIP, event.DstIP)
	case 136:
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 neighbor advertisement %s -> %s", event.SrcIP, event.DstIP)
	default:
		event.Summary = fmt.Sprintf("IPv6 ICMPv6 type %d code %d %s -> %s", payload[0], payload[1], event.SrcIP, event.DstIP)
	}
}

func parsePorts(event *Event, payload []byte, prefix string) {
	if len(payload) < 4 {
		event.Summary = fmt.Sprintf("%s truncated %s -> %s", prefix, event.SrcIP, event.DstIP)
		return
	}
	event.SrcPort = int(binary.BigEndian.Uint16(payload[0:2]))
	event.DstPort = int(binary.BigEndian.Uint16(payload[2:4]))
	event.Summary = fmt.Sprintf("%s %s:%d -> %s:%d", prefix, event.SrcIP, event.SrcPort, event.DstIP, event.DstPort)
}

func ipProtocolName(proto byte) string {
	switch proto {
	case 1:
		return "ICMP"
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 58:
		return "ICMPv6"
	default:
		return fmt.Sprintf("%d", proto)
	}
}

func etherTypeName(etherType uint16) string {
	switch etherType {
	case 0x0800:
		return "IPv4"
	case 0x0806:
		return "ARP"
	case 0x0842:
		return "Wake-on-LAN"
	case 0x22f0:
		return "AVTP"
	case 0x6003:
		return "DECnet"
	case 0x8035:
		return "RARP"
	case 0x809b:
		return "AppleTalk"
	case 0x80f3:
		return "AARP"
	case 0x8100:
		return "VLAN"
	case 0x86dd:
		return "IPv6"
	case 0x8808:
		return "Ethernet Flow Control"
	case 0x8819:
		return "CobraNet"
	case 0x8847:
		return "MPLS unicast"
	case 0x8848:
		return "MPLS multicast"
	case 0x8863:
		return "PPPoE discovery"
	case 0x8864:
		return "PPPoE session"
	case 0x8870:
		return "Jumbo Frames"
	case 0x888e:
		return "EAPOL"
	case 0x88a8:
		return "Provider Bridge VLAN"
	case 0x88cc:
		return "LLDP"
	case 0x88e5:
		return "MACsec"
	case 0x88f7:
		return "PTP"
	default:
		return "Unknown"
	}
}
