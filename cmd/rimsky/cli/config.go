// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

type Config struct {
	CurrentContext string             `yaml:"current_context,omitempty"`
	Contexts       map[string]Context `yaml:"contexts,omitempty"`
}

type Context struct {
	Endpoint string `yaml:"endpoint"`
	APIKey   string `yaml:"api_key,omitempty"`
}

var contextNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)

func ValidContextName(s string) bool { return contextNamePattern.MatchString(s) }

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rimsky", "config.yml"), nil
}

func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Contexts: map[string]Context{}}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := configload.LoadFile(path, &cfg); err != nil {
		return nil, err
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	for name := range cfg.Contexts {
		if !ValidContextName(name) {
			return fmt.Errorf("invalid context name %q", name)
		}
	}
	if cfg.CurrentContext != "" && !ValidContextName(cfg.CurrentContext) {
		return fmt.Errorf("invalid current_context %q", cfg.CurrentContext)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
