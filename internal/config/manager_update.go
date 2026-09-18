package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Clone returns an independent configuration snapshot suitable for validation
// and mutation before it is persisted.
func Clone(cfg *Config) *Config {
	return cloneConfig(cfg)
}

// Normalized validates and resolves a configuration without mutating the caller.
func Normalized(cfg *Config) (*Config, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is nil")
	}
	next := cloneConfig(cfg)
	if err := validateModelConfig(next); err != nil {
		return nil, err
	}
	normalizeConfig(next)
	return next, nil
}

// Replace validates through normalization, writes the new configuration, and
// only then exposes it to readers. It is used by runtime APIs that must avoid
// a partially-updated in-memory configuration when persistence fails.
func (m *Manager) Replace(next *Config) error {
	if m == nil || next == nil {
		return fmt.Errorf("configuration replacement is nil")
	}

	candidate := cloneConfig(next)
	if err := validateModelConfig(candidate); err != nil {
		return err
	}
	normalizeConfig(candidate)
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.homeDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(m.cfgPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	m.config = candidate
	return nil
}
