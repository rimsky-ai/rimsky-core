// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: role-template
package roles

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.json
var FS embed.FS

func Load(name string) ([]byte, bool) {
	data, err := FS.ReadFile(name + ".json")
	if err != nil {
		return nil, false
	}
	return data, true
}

func AllNames() []string {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	sort.Strings(out)
	return out
}
