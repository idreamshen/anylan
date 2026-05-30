package tap

import "github.com/songgao/water"

type DeviceInfo struct {
	Name       string `json:"name"`
	Display    string `json:"display"`
	Default    bool   `json:"default,omitempty"`
	Virtual    bool   `json:"virtual,omitempty"`
	Selectable bool   `json:"selectable"`
}

type Device struct {
	iface *water.Interface
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
