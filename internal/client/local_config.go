package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/idreamshen/anylan/internal/tap"
)

const clientConfigFile = "client.json"

type localClientConfig struct {
	Server                   string `json:"server,omitempty"`
	Room                     string `json:"room,omitempty"`
	DisplayName              string `json:"display_name,omitempty"`
	DeviceName               string `json:"device_name,omitempty"`
	InsecureSkipVerify       bool   `json:"insecure_skip_verify"`
	PrioritizeVirtualAdapter *bool  `json:"prioritize_virtual_adapter,omitempty"`
}

func defaultControlConfig() Config {
	return Config{
		DeviceName:               tap.DefaultDeviceName(),
		PrioritizeVirtualAdapter: true,
	}
}

func defaultClientConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "anylan", clientConfigFile), nil
}

func loadClientConfig(path string) (Config, error) {
	cfg := defaultControlConfig()
	if path == "" {
		var err error
		path, err = defaultClientConfigPath()
		if err != nil {
			return cfg, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var stored localClientConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		return cfg, err
	}
	cfg.Server = stored.Server
	cfg.Room = stored.Room
	cfg.DisplayName = stored.DisplayName
	cfg.DeviceName = stored.DeviceName
	cfg.InsecureSkipVerify = stored.InsecureSkipVerify
	if stored.PrioritizeVirtualAdapter != nil {
		cfg.PrioritizeVirtualAdapter = *stored.PrioritizeVirtualAdapter
	}
	if cfg.DeviceName == "" {
		cfg.DeviceName = tap.DefaultDeviceName()
	}
	return cfg, nil
}

func saveClientConfig(path string, cfg Config) error {
	if path == "" {
		var err error
		path, err = defaultClientConfigPath()
		if err != nil {
			return err
		}
	}
	prioritize := cfg.PrioritizeVirtualAdapter
	stored := localClientConfig{
		Server:                   cfg.Server,
		Room:                     cfg.Room,
		DisplayName:              cfg.DisplayName,
		DeviceName:               cfg.DeviceName,
		InsecureSkipVerify:       cfg.InsecureSkipVerify,
		PrioritizeVirtualAdapter: &prioritize,
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
