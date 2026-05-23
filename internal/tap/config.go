package tap

import (
	"fmt"
	"net"
)

func IPv4AndMask(cidr string) (string, string, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return "", "", fmt.Errorf("not an IPv4 CIDR: %s", cidr)
	}
	mask := network.Mask
	if len(mask) != net.IPv4len {
		return "", "", fmt.Errorf("not an IPv4 netmask: %s", cidr)
	}
	return ipv4.String(), net.IP(mask).String(), nil
}
