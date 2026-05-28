// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package roles embeds the bundled role JSONs the CLI expands into
// permission grants at `rimsky auth create-key` time. Server-side
// rimsky has no concept of roles; this surface is CLI-only.
//
// @concept: role-template
package roles

import (
	"embed"
	"sort"
)

//go:embed *.json
var FS embed.FS

// Load returns the bundled role JSON by name (without ".json").
// Returns ("", false) if not found.
func Load(name string) ([]byte, bool) {
	data, err := FS.ReadFile(name + ".json")
	if err != nil {
		return nil, false
	}
	return data, true
}

// AllNames returns the bundled role names sorted.
func AllNames() []string {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		n := e.Name()
		if len(n) > 5 && n[len(n)-5:] == ".json" {
			out = append(out, n[:len(n)-5])
		}
	}
	sort.Strings(out)
	return out
}
