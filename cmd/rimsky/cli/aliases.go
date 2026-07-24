// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: rimsky
package cli

import (
	"os"
	"path/filepath"

	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

type aliasFile struct {
	Aliases map[string]string `yaml:"aliases"`
}

func LoadServiceAliases() map[string]string {
	merged := map[string]string{}
	if home, err := os.UserHomeDir(); err == nil {
		loadAliasFile(filepath.Join(home, ".rimsky", "aliases.yml"), merged)
	}
	loadAliasFile(filepath.Join(".rimsky", "aliases.yml"), merged)
	return merged
}

func loadAliasFile(path string, into map[string]string) {
	var f aliasFile
	if err := configload.LoadFile(path, &f); err != nil {
		return
	}
	for k, v := range f.Aliases {
		into[k] = v
	}
}
