// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: rimsky (CLI --service alias resolution)
package cli

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f aliasFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return
	}
	for k, v := range f.Aliases {
		into[k] = v
	}
}
