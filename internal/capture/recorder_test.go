package capture

import (
	"strings"
	"testing"
)

func TestRecorderKeepsRecentEvents(t *testing.T) {
	recorder := NewRecorder(2)
	frame := []byte{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0x02, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06,
	}
	recorder.Record(Metadata{Direction: "tx", Room: "room-a", PeerID: "peer-a", PeerName: "alice"}, frame)
	recorder.Record(Metadata{Direction: "rx"}, frame)
	recorder.Record(Metadata{Direction: "tx"}, frame)

	snap := recorder.Snapshot()
	if snap.Count != 2 {
		t.Fatalf("expected 2 events, got %d", snap.Count)
	}
	if snap.Events[0].Seq != 2 || snap.Events[1].Seq != 3 {
		t.Fatalf("unexpected seqs: %#v", snap.Events)
	}
	if snap.Events[1].TypeName != "ARP" || snap.Events[1].EtherType != "0x0806" {
		t.Fatalf("unexpected frame type: %#v", snap.Events[1])
	}
}

func TestRecordSummarizesARPRequest(t *testing.T) {
	recorder := NewRecorder(10)
	recorder.Record(Metadata{Direction: "tx"}, ethernet(0x0806, []byte{
		0x00, 0x01, 0x08, 0x00, 0x06, 0x04, 0x00, 0x01,
		0x02, 0x00, 0x00, 0x00, 0x00, 0x01,
		10, 240, 0, 2,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		10, 240, 0, 3,
	}))

	event := recorder.Snapshot().Events[0]
	if event.Summary != "ARP request who-has 10.240.0.3 tell 10.240.0.2" || event.SrcIP != "10.240.0.2" || event.DstIP != "10.240.0.3" {
		t.Fatalf("unexpected arp summary: %#v", event)
	}
}

func TestRecordSummarizesIPv4ICMPEcho(t *testing.T) {
	recorder := NewRecorder(10)
	payload := append(ipv4Header(1, [4]byte{10, 240, 0, 2}, [4]byte{10, 240, 0, 3}), 8, 0, 0, 0)
	recorder.Record(Metadata{Direction: "tx"}, ethernet(0x0800, payload))

	event := recorder.Snapshot().Events[0]
	if !strings.Contains(event.Summary, "IPv4 ICMP echo request (ping)") || event.IPProto != "ICMP" {
		t.Fatalf("unexpected icmp summary: %#v", event)
	}
}

func TestRecordSummarizesIPv4TCPAndUDP(t *testing.T) {
	tests := []struct {
		name    string
		proto   byte
		ports   []byte
		summary string
		ipProto string
	}{
		{name: "tcp", proto: 6, ports: []byte{0x30, 0x39, 0x01, 0xbb}, summary: "IPv4 TCP 10.240.0.2:12345 -> 10.240.0.3:443", ipProto: "TCP"},
		{name: "udp", proto: 17, ports: []byte{0x14, 0xe9, 0x27, 0x10}, summary: "IPv4 UDP 10.240.0.2:5353 -> 10.240.0.3:10000", ipProto: "UDP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRecorder(10)
			payload := append(ipv4Header(tt.proto, [4]byte{10, 240, 0, 2}, [4]byte{10, 240, 0, 3}), tt.ports...)
			recorder.Record(Metadata{Direction: "tx"}, ethernet(0x0800, payload))

			event := recorder.Snapshot().Events[0]
			if event.Summary != tt.summary || event.IPProto != tt.ipProto {
				t.Fatalf("unexpected %s summary: %#v", tt.name, event)
			}
		})
	}
}

func TestRecordSummarizesUnknownEtherType(t *testing.T) {
	recorder := NewRecorder(10)
	recorder.Record(Metadata{Direction: "tx"}, ethernet(0x88b5, []byte("sample")))

	event := recorder.Snapshot().Events[0]
	if event.Summary != "Unknown EtherType 0x88b5" {
		t.Fatalf("unexpected unknown summary: %#v", event)
	}
}

func TestRecordParsesVLANInnerEtherType(t *testing.T) {
	recorder := NewRecorder(10)
	frame := []byte{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0x02, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x81, 0x00,
		0x00, 0x64,
		0x86, 0xdd,
	}
	recorder.Record(Metadata{Direction: "ingress"}, frame)

	event := recorder.Snapshot().Events[0]
	if !event.Valid || !event.VLAN || event.TypeName != "IPv6" || event.EtherType != "0x86dd" {
		t.Fatalf("unexpected vlan event: %#v", event)
	}
}

func TestRecordMarksShortFrameInvalid(t *testing.T) {
	recorder := NewRecorder(10)
	recorder.Record(Metadata{Direction: "rx"}, []byte{1, 2, 3})

	event := recorder.Snapshot().Events[0]
	if event.Valid || event.Reason != "frame too short" {
		t.Fatalf("unexpected short frame event: %#v", event)
	}
}

func ethernet(etherType uint16, payload []byte) []byte {
	frame := []byte{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0x02, 0x00, 0x00, 0x00, 0x00, 0x01,
		byte(etherType >> 8), byte(etherType),
	}
	return append(frame, payload...)
}

func ipv4Header(proto byte, src, dst [4]byte) []byte {
	return []byte{
		0x45, 0x00, 0x00, 0x18,
		0x00, 0x00, 0x00, 0x00,
		0x40, proto, 0x00, 0x00,
		src[0], src[1], src[2], src[3],
		dst[0], dst[1], dst[2], dst[3],
	}
}
