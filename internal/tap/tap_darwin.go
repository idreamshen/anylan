//go:build darwin

package tap

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/songgao/water"
)

func DefaultDeviceName() string {
	return ""
}

func Open(name string) (*Device, error) {
	cfg := water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:   strings.TrimSpace(name),
			Driver: water.MacOSDriverSystem,
		},
	}
	iface, err := water.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Device{iface: iface, layer: LayerIP}, nil
}

func ListDevices() ([]DeviceInfo, error) {
	devices := []DeviceInfo{{
		Name:       "",
		Display:    "Auto-detect utun adapter",
		Default:    true,
		Virtual:    true,
		Selectable: true,
	}}
	ifaces, err := net.Interfaces()
	if err != nil {
		return devices, err
	}
	for _, iface := range ifaces {
		devices = append(devices, DeviceInfo{
			Name:       iface.Name,
			Display:    iface.Name,
			Virtual:    strings.HasPrefix(iface.Name, "utun"),
			Selectable: strings.HasPrefix(iface.Name, "utun"),
		})
	}
	return devices, nil
}

func Configure(ctx context.Context, name, _ string, cidr string, mtu int, prioritize bool) error {
	ip, mask, err := IPv4AndMask(cidr)
	if err != nil {
		return err
	}

	commands := [][]string{
		{"ifconfig", name, "inet", ip, ip, "netmask", mask, "mtu", strconv.Itoa(mtu), "up"},
		{"route", "add", "-net", networkCIDR(cidr), "-interface", name},
	}
	if prioritize {
		commands = append(commands, []string{"ifconfig", name, "metric", "1"})
	}

	for i, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil && !(i == 1 && strings.Contains(string(output), "File exists")) {
			return fmt.Errorf("%v failed: %w: %s; run anylan-client with sudo", args, err, output)
		}
	}
	return nil
}

func networkCIDR(cidr string) string {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return cidr
	}
	return network.String()
}
