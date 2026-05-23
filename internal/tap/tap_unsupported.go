//go:build !linux && !windows

package tap

import (
	"context"
	"fmt"
)

func DefaultDeviceName() string {
	return ""
}

func Open(string) (*Device, error) {
	return nil, fmt.Errorf("anylan client TAP devices are only supported on Linux and Windows")
}

func Configure(context.Context, string, string, string, int) error {
	return fmt.Errorf("anylan client TAP devices are only supported on Linux and Windows")
}
