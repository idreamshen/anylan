//go:build linux

package tap

import (
	"context"
	"fmt"
	"os/exec"
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
	return &Device{iface: iface}, nil
}

func Configure(ctx context.Context, name, mac, cidr string, mtu int) error {
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
