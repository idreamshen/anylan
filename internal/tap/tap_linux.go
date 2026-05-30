//go:build linux

package tap

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/songgao/water"
)

func DefaultDeviceName() string {
	return "anylan0"
}

func Open(name string) (*Device, error) {
	if name == "" {
		name = DefaultDeviceName()
	}
	cfg := water.Config{
		DeviceType: water.TAP,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name: name,
		},
	}
	iface, err := water.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Device{iface: iface, layer: LayerEthernet}, nil
}

func ListDevices() ([]DeviceInfo, error) {
	defaultName := DefaultDeviceName()
	seen := map[string]bool{}
	devices := []DeviceInfo{{
		Name:       defaultName,
		Display:    defaultName + " (default)",
		Default:    true,
		Virtual:    true,
		Selectable: true,
	}}
	seen[defaultName] = true

	ifaces, err := net.Interfaces()
	if err != nil {
		return devices, err
	}
	for _, iface := range ifaces {
		if seen[iface.Name] {
			continue
		}
		virtual, selectable := linuxInterfaceKind(iface.Name)
		devices = append(devices, DeviceInfo{
			Name:       iface.Name,
			Display:    iface.Name,
			Virtual:    virtual,
			Selectable: selectable,
		})
		seen[iface.Name] = true
	}
	return devices, nil
}

func linuxInterfaceKind(name string) (bool, bool) {
	if name == "" {
		return false, false
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", name, "tun_flags")); err == nil {
		return true, true
	}
	if target, err := os.Readlink(filepath.Join("/sys/class/net", name)); err == nil {
		virtual := filepath.IsAbs(target) && filepath.Base(filepath.Dir(target)) == "virtual" || filepath.Base(filepath.Dir(filepath.Dir(target))) == "virtual"
		return virtual, false
	}
	return false, false
}

func Configure(ctx context.Context, name, mac, cidr string, mtu int, _ bool) error {
	commands := [][]string{
		{"ip", "link", "set", "dev", name, "address", mac},
		{"ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)},
		{"ip", "addr", "add", cidr, "dev", name},
		{"ip", "link", "set", "dev", name, "up"},
	}

	for _, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v failed: %w: %s", args, err, output)
		}
	}
	return nil
}
