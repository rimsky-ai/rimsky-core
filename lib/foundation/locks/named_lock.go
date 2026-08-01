// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package locks

import "fmt"

type NamedLockSpec struct {
	Name         string
	TemplateName string
}

type NamedLockConfig struct {
	Limit int `yaml:"limit"`
}

type NamedLocksConfig struct {
	Locks map[string]NamedLockConfig
}

func (c NamedLocksConfig) Get(name string) (NamedLockConfig, bool) {
	if c.Locks == nil {
		return NamedLockConfig{}, false
	}
	cfg, ok := c.Locks[name]
	return cfg, ok
}

// @concept: named-lock
func (c NamedLocksConfig) Validate() error {
	for name, cfg := range c.Locks {
		if cfg.Limit < 1 {
			return fmt.Errorf("named_locks[%q]: limit must be >= 1, got %d", name, cfg.Limit)
		}
	}
	return nil
}
