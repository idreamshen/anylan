//go:build windows

package tap

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"

	"github.com/songgao/water"
)

func DefaultDeviceName() string {
	return ""
}

// Open opens a Windows TAP adapter.  It searches for any installed adapter
// whose ComponentId is "tap0901" or "root\tap0901", regardless of its current
// interface name.  The adapter is used as-is without renaming.
func Open(name string) (*Device, error) {
	var lastErr error
	for _, componentID := range []string{"tap0901", `root\tap0901`} {
		cfg := water.Config{
			DeviceType: water.TAP,
			PlatformSpecificParams: water.PlatformSpecificParams{
				ComponentID: componentID,
				// Leave InterfaceName empty so water finds any adapter with
				// the matching ComponentId, independent of its current name.
				InterfaceName: "",
			},
		}
		iface, err := water.New(cfg)
		if err != nil {
			lastErr = err
			continue
		}
		return &Device{iface: iface}, nil
	}
	return nil, fmt.Errorf("open Windows TAP adapter failed: %w; install an OpenVPN/tap-windows6 compatible TAP driver and run anylan-client from an Administrator shell", lastErr)
}

func ListDevices() ([]DeviceInfo, error) {
	devices := []DeviceInfo{{
		Name:       "",
		Display:    "Auto-detect TAP adapter",
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
			Selectable: true,
		})
	}
	return devices, nil
}

func Configure(ctx context.Context, name, _ string, cidr string, mtu int) error {
	ip, mask, err := IPv4AndMask(cidr)
	if err != nil {
		return err
	}

	commands := [][]string{
		{"netsh", "interface", "ip", "set", "address", name, "static", ip, mask},
		{"netsh", "interface", "ipv4", "set", "subinterface", name, "mtu=" + strconv.Itoa(mtu), "store=active"},
	}

	for _, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v failed: %w: %s; run anylan-client from an Administrator shell", args, err, output)
		}
	}
	return nil
}
