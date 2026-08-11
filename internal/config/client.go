// Package config handles the portly-client YAML config file. The client
// intentionally only needs a server address, token, and pinned CA
// fingerprint — tunnel definitions themselves live server-side and are
// pushed to the client after authentication.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ClientConfig struct {
	ServerAddr    string `yaml:"server_addr"`
	Token         string `yaml:"token"`
	CAFingerprint string `yaml:"ca_fingerprint"`
	// APIBase is the server's HTTP(S) base URL (e.g. "https://panel.example.com"),
	// used only for self-update checks — not required for tunnel operation, so
	// configs written before this field existed simply have self-update disabled
	// until re-enrolled. Empty for configs written via 'portly-client init'.
	APIBase string `yaml:"api_base,omitempty"`
}

func LoadClientConfig(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ClientConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ServerAddr == "" || cfg.Token == "" || cfg.CAFingerprint == "" {
		return nil, fmt.Errorf("config missing required fields (server_addr, token, ca_fingerprint)")
	}
	return &cfg, nil
}

func SaveClientConfig(path string, cfg *ClientConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
