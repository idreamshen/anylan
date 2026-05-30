package tap

import (
	"fmt"
	"net"

	"github.com/songgao/water"
)

type DeviceInfo struct {
	Name       string `json:"name"`
	Display    string `json:"display"`
	Default    bool   `json:"default,omitempty"`
	Virtual    bool   `json:"virtual,omitempty"`
	Selectable bool   `json:"selectable"`
}

type Layer int

const (
	LayerEthernet Layer = iota + 1
	LayerIP
)

func (l Layer) String() string {
	switch l {
	case LayerEthernet:
		return "ethernet"
	case LayerIP:
		return "ip"
	default:
		return "unknown"
	}
}

type Device struct {
	iface *water.Interface
	layer Layer
}

func (d *Device) Name() string {
	return d.iface.Name()
}

func (d *Device) Layer() Layer {
	if d.layer == 0 {
		return LayerEthernet
	}
	return d.layer
}

func (d *Device) HardwareAddr() (net.HardwareAddr, error) {
	iface, err := net.InterfaceByName(d.Name())
	if err != nil {
		return nil, err
	}
	if len(iface.HardwareAddr) != 6 {
		return nil, fmt.Errorf("interface %s has no ethernet MAC address", d.Name())
	}
	return append(net.HardwareAddr(nil), iface.HardwareAddr...), nil
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
