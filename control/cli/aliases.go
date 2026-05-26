// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// aliases.go — client-side `--service` alias resolution. A bare
// `--service <name>` on `rimsky run` resolves the name to a binary path via
// two optional alias files: ~/.rimsky/aliases.yml (global) overlaid by
// .rimsky/aliases.yml (project-local; later wins). These are pure CLI
// sugar; the server never sees aliases — the resolved path is what lands in
// the instance's service_bindings. Per spec 2026-05-24-host-agent-and-proxy-design.md.
//
// @concept: rimsky (CLI --service alias resolution)
package cli

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// aliasFile is the on-disk shape of an aliases.yml file.
type aliasFile struct {
	Aliases map[string]string `yaml:"aliases"`
}

// LoadServiceAliases merges the global (~/.rimsky/aliases.yml) and
// project-local (.rimsky/aliases.yml) alias files. Project-local entries
// overlay (override) global entries with the same name. Missing files are
// fine — they contribute nothing. Returns a possibly-empty (never nil) map.
func LoadServiceAliases() map[string]string {
	merged := map[string]string{}
	if home, err := os.UserHomeDir(); err == nil {
		loadAliasFile(filepath.Join(home, ".rimsky", "aliases.yml"), merged)
	}
	loadAliasFile(filepath.Join(".rimsky", "aliases.yml"), merged)
	return merged
}

// loadAliasFile reads path and overlays its aliases into `into`. A missing
// or unparseable file is silently skipped (best-effort overlay).
func loadAliasFile(path string, into map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // missing is fine
	}
	var f aliasFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return // malformed alias file: skip rather than fail the run
	}
	for k, v := range f.Aliases {
		into[k] = v
	}
}
