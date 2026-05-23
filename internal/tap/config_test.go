package tap

import "testing"

func TestIPv4AndMask(t *testing.T) {
	ip, mask, err := IPv4AndMask("10.240.0.2/24")
	if err != nil {
		t.Fatalf("IPv4AndMask failed: %v", err)
	}
	if ip != "10.240.0.2" {
		t.Fatalf("ip = %q, want 10.240.0.2", ip)
	}
	if mask != "255.255.255.0" {
		t.Fatalf("mask = %q, want 255.255.255.0", mask)
	}
}

func TestIPv4AndMaskRejectsIPv6(t *testing.T) {
	if _, _, err := IPv4AndMask("fd00::1/64"); err == nil {
		t.Fatal("IPv4AndMask accepted IPv6 CIDR")
	}
}

func TestDefaultDeviceNameIsCallable(t *testing.T) {
	_ = DefaultDeviceName()
}
