//go:build linux

package tap

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/songgao/water"
)

type Device struct {
	iface *water.Interface
}

func Open(name string) (*Device, error) {
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

func (d *Device) Name() string {
	return d.iface.Name()
}

func (d *Device) Read(p []byte) (int, error) {
	return d.iface.Read(p)
}

func (d *Device) Write(p []byte) (int, error) {
	return d.iface.Write(p)
}

func (d *Device) Close() error {
	return d.iface.Close()
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
