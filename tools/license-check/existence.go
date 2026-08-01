// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func verifyEntriesExist(cfg *licensingConfig, root string) []violation {
	var out []violation
	check := func(bucket string, prefixes []string) {
		for _, p := range prefixes {
			if p == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); os.IsNotExist(err) {
				out = append(out, violation{
					path:    "licensing.yml",
					message: fmt.Sprintf("%s entry %q lists a path that no longer exists", bucket, p),
				})
			}
		}
	}
	check("apache", cfg.apachePrefixes)
	check("agpl", cfg.agplPrefixes)
	check("exempt", cfg.exemptEntries)
	return out
}
