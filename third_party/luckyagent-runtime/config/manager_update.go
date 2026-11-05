package config

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
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
	// Write a complete temporary file and rename it into place. A watcher or a
	// second process must never observe a partially-written JSON document.
	tmp, err := os.CreateTemp(m.homeDir, ".config.json-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, m.cfgPath); err != nil {
		// Windows does not allow Rename to replace an existing file. Keep the
		// atomic path on Unix and use the platform-compatible fallback there.
		if runtime.GOOS != "windows" {
			return fmt.Errorf("replace config: %w", err)
		}
		if removeErr := os.Remove(m.cfgPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace config: %w", err)
		}
		if renameErr := os.Rename(tmpPath, m.cfgPath); renameErr != nil {
			return fmt.Errorf("replace config: %w", renameErr)
		}
	}
	m.config = candidate
	return nil
}
