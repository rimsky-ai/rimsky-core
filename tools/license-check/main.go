// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "license-check: walker matched 0 source files under %q — check --root and licensing.yml classification/exempt entries\n", *root)
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
	violations = append(violations, verifyApacheClosure(cfg, *root)...)

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
