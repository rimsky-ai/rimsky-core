// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-license-check verifies the per-file license headers and the
// import-graph boundary defined by licensing.yml at the repo root.
//
// Usage:
//
//	rimsky-license-check                  # verify; exit 1 on violations
//	rimsky-license-check --stamp          # add missing headers in place
//
// The boundary rule: Apache-classified Go files cannot import
// AGPL-classified packages. AGPL files may import Apache freely. Header
// text on every source file must match the file's classification. Every
// apache/agpl entry in licensing.yml must point at a path that exists, and
// every source file must be classified — the two checks keep licensing.yml
// in exact correspondence with the tree.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	stamp := flag.Bool("stamp", false, "Add missing license headers in place instead of verifying.")
	root := flag.String("root", ".", "Repo root (where licensing.yml lives).")
	flag.Parse()

	cfg, err := loadLicensingYAML(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "license-check: %v\n", err)
		os.Exit(2)
	}

	files, err := walkSourceFiles(*root, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "license-check: %v\n", err)
		os.Exit(2)
	}

	if *stamp {
		stamped, err := stampHeaders(files)
		if err != nil {
			fmt.Fprintf(os.Stderr, "license-check: stamp: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("license-check: stamped %d files\n", stamped)
		return
	}

	violations := verifyHeaders(files)
	violations = append(violations, verifyImports(files, cfg)...)
	violations = append(violations, verifyEntriesExist(cfg, *root)...)

	apacheCount, agplCount := 0, 0
	for _, f := range files {
		if f.classification == classApache {
			apacheCount++
		} else if f.classification == classAGPL {
			agplCount++
		}
	}

	if len(violations) == 0 {
		fmt.Printf("license-check: %d apache files, %d agpl files, 0 violations\n", apacheCount, agplCount)
		return
	}

	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "license-check: %s: %s\n", v.path, v.message)
	}
	fmt.Fprintf(os.Stderr, "license-check: %d apache files, %d agpl files, %d violations\n", apacheCount, agplCount, len(violations))
	os.Exit(1)
}
