package client

import (
	"net"
	"testing"

	"github.com/idreamshen/anylan/internal/protocol"
)

func TestEthernetFrameFromIPv4(t *testing.T) {
	packet := []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 1, 0, 0, 10, 240, 0, 2, 10, 240, 0, 3}
	dst := net.HardwareAddr{0x02, 0, 0, 0, 0, 3}
	src := net.HardwareAddr{0x02, 0, 0, 0, 0, 2}

	frame := ethernetFrameFromIPv4(packet, dst, src)
	if len(frame) != 14+len(packet) {
		t.Fatalf("frame length = %d, want %d", len(frame), 14+len(packet))
	}
	if !isIPv4EthernetFrame(frame) {
		t.Fatal("frame was not recognised as IPv4 ethernet")
	}
	if !ipv4Dst(frame[14:]).Equal(net.IPv4(10, 240, 0, 3)) {
		t.Fatalf("dst = %s", ipv4Dst(frame[14:]))
	}
}

func TestStatusPeerMACForIPv4(t *testing.T) {
	status := newStatus(Config{})
	status.ipv4 = "10.240.0.2"
	status.peers = []protocol.PeerInfo{
		{IPv4: "10.240.0.2", MAC: "02:00:00:00:00:02"},
		{IPv4: "10.240.0.3", MAC: "02:00:00:00:00:03"},
	}

	mac := status.peerMACForIPv4(net.IPv4(10, 240, 0, 3))
	if mac.String() != "02:00:00:00:00:03" {
		t.Fatalf("mac = %s", mac)
	}
	if got := status.peerMACForIPv4(net.IPv4(10, 240, 0, 4)); got != nil {
		t.Fatalf("unexpected mac = %s", got)
	}
}

func TestShouldDeliverIPv4ToTUN(t *testing.T) {
	packet := []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 10, 240, 0, 3, 10, 240, 0, 255}
	if !shouldDeliverIPv4ToTUN(packet, net.IPv4(10, 240, 0, 2), broadcastIPv4("10.240.0.2/24")) {
		t.Fatal("room broadcast was not delivered")
	}
	packet[16] = 10
	packet[17] = 240
	packet[18] = 0
	packet[19] = 4
	if shouldDeliverIPv4ToTUN(packet, net.IPv4(10, 240, 0, 2), broadcastIPv4("10.240.0.2/24")) {
		t.Fatal("unicast to another peer was delivered")
	}
}

func TestARPReply(t *testing.T) {
	ownMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 2}
	peerMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 3}
	ownIP := net.IPv4(10, 240, 0, 2)
	peerIP := net.IPv4(10, 240, 0, 3)
	request := arpRequest(peerMAC, peerIP, ownIP)

	if !isARPRequestForIPv4(request, ownIP) {
		t.Fatal("request was not recognised")
	}
	reply := arpReply(request, ownMAC, ownIP)
	if got := net.HardwareAddr(reply[0:6]).String(); got != peerMAC.String() {
		t.Fatalf("reply dst mac = %s", got)
	}
	if got := net.HardwareAddr(reply[6:12]).String(); got != ownMAC.String() {
		t.Fatalf("reply src mac = %s", got)
	}
	if op := reply[21]; op != 2 {
		t.Fatalf("arp op low byte = %d", op)
	}
}

func arpRequest(srcMAC net.HardwareAddr, srcIP, targetIP net.IP) []byte {
	frame := make([]byte, 42)
	copy(frame[0:6], broadcastMAC())
	copy(frame[6:12], srcMAC)
	frame[12] = 0x08
	frame[13] = 0x06
	frame[15] = 1
	frame[16] = 0x08
	frame[18] = 6
	frame[19] = 4
	frame[21] = 1
	copy(frame[22:28], srcMAC)
	copy(frame[28:32], srcIP.To4())
	copy(frame[38:42], targetIP.To4())
	return frame
}
