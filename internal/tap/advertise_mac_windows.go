//go:build windows

package tap

func ShouldAdvertiseMAC() bool {
	return true
}

func AdvertiseMAC(device *Device) (string, error) {
	mac, err := device.HardwareAddr()
	if err != nil {
		return "", err
	}
	return mac.String(), nil
}
