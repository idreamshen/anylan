//go:build !windows

package tap

func ShouldAdvertiseMAC() bool {
	return false
}

func AdvertiseMAC(*Device) (string, error) {
	return "", nil
}
