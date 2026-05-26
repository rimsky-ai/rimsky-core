// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// config.go — ~/.rimsky/config.yml load/save.
//
// File format (per spec §4.2):
//
//	current_context: dev
//	contexts:
//	  dev:     { endpoint: http://localhost:8080 }
//	  staging: { endpoint: https://rimsky.staging.example.com }
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape of ~/.rimsky/config.yml.
type Config struct {
	CurrentContext string             `yaml:"current_context,omitempty"`
	Contexts       map[string]Context `yaml:"contexts,omitempty"`
}

// Context is one named entry in Config.Contexts.
type Context struct {
	Endpoint string `yaml:"endpoint"`
	// APIKey is the Bearer token the CLI presents on authenticated
	// requests, populated by `rimsky auth login` and consumed by the
	// host-agent for outbound authentication to the host-agent-proxy.
	// Existing configs without this key continue to load — YAML tolerates
	// the missing field; omitempty keeps it out of serialized output when
	// unset. Per spec 2026-05-24-host-agent-and-proxy-design.md.
	APIKey string `yaml:"api_key,omitempty"`
}

// contextNamePattern is the spec §2.3 / §4.2 context-name regex.
var contextNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)

// ValidContextName reports whether s is a syntactically valid context
// name. Used by SaveConfig and ctx-CRUD operations.
func ValidContextName(s string) bool { return contextNamePattern.MatchString(s) }

// DefaultConfigPath returns ~/.rimsky/config.yml using os.UserHomeDir.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rimsky", "config.yml"), nil
}

// LoadConfig reads path and unmarshals it. Returns &Config{} (not nil)
// when the file does not exist.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Contexts: map[string]Context{}}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return &cfg, nil
}

// SaveConfig writes cfg to path. Creates parent directories if missing.
// Validates context names before writing.
func SaveConfig(path string, cfg *Config) error {
	for name := range cfg.Contexts {
		if !ValidContextName(name) {
			return fmt.Errorf("invalid context name %q", name)
		}
	}
	if cfg.CurrentContext != "" && !ValidContextName(cfg.CurrentContext) {
		return fmt.Errorf("invalid current_context %q", cfg.CurrentContext)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
