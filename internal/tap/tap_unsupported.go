//go:build !linux && !windows && !darwin

package tap

import (
	"context"
	"fmt"
)

func DefaultDeviceName() string {
	return ""
}

func Open(string) (*Device, error) {
	return nil, fmt.Errorf("anylan client TAP devices are only supported on Linux, macOS, and Windows")
}

func ListDevices() ([]DeviceInfo, error) {
	return nil, fmt.Errorf("anylan client TAP devices are only supported on Linux, macOS, and Windows")
}

func Configure(context.Context, string, string, string, int, bool) error {
	return fmt.Errorf("anylan client TAP devices are only supported on Linux, macOS, and Windows")
}
