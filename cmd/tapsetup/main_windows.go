//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const componentID = "tap0901"

const tapDriverKey = `SYSTEM\CurrentControlSet\Control\Class\{4D36E972-E325-11CE-BFC1-08002BE10318}`

func main() {
	if len(os.Args) != 2 || os.Args[1] != "ensure" {
		fmt.Fprintln(os.Stderr, "usage: anylan-tapsetup ensure")
		os.Exit(2)
	}

	if err := ensureAdapter(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func ensureAdapter() error {
	if hasAdapter() {
		return nil
	}

	driverDir, err := driverDir()
	if err != nil {
		return err
	}
	devconPath := filepath.Join(driverDir, "devcon.exe")
	infPath := filepath.Join(driverDir, "OemVista.inf")

	args := []string{"install", infPath, componentID}
	cmd := exec.Command(devconPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create TAP adapter failed: %s %s: %w: %s", devconPath, strings.Join(args, " "), err, output)
	}

	if !hasAdapter() {
		return errors.New("create TAP adapter failed: adapter was not found after devcon completed")
	}
	return nil
}

func hasAdapter() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, tapDriverKey, registry.READ)
	if err != nil {
		return false
	}
	defer k.Close()

	keys, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return false
	}
	for _, subkey := range keys {
		adapterKey, err := registry.OpenKey(registry.LOCAL_MACHINE, tapDriverKey+`\`+subkey, registry.READ)
		if err != nil {
			continue
		}
		value, _, err := adapterKey.GetStringValue("ComponentId")
		adapterKey.Close()
		if err == nil && (strings.EqualFold(value, componentID) || strings.EqualFold(value, `root\`+componentID)) {
			return true
		}
	}
	return false
}

func driverDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(exe), "tap-windows6", "amd64")
	for _, name := range []string{"devcon.exe", "OemVista.inf", "tap0901.cat", "tap0901.sys"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("TAP driver file not found at %s: %w", path, err)
		}
	}
	return dir, nil
}
