// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// existence.go — verify that every classification entry in licensing.yml
// points at a path that actually exists. This is the mirror of the walker's
// classUnknown check: the walker flags existing source that ISN'T
// classified; this flags classification entries that no longer exist (dirs
// moved out or deleted). Together they keep licensing.yml in exact
// correspondence with the tree.
//
// Only the apache and agpl lists are checked. The exempt list is a set of
// skip directives that legitimately reference paths absent from a clean
// checkout — build outputs (bin/) and optional tooling dirs (.elves/) — so
// its entries are not required to exist.

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// verifyEntriesExist flags apache/agpl licensing.yml entries whose path is
// not present under root.
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
	return out
}
