// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main.go — rimsky-docs-glossary. Reads docs/concepts/*.md frontmatter
// and emits docs/glossary.md.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
)

func main() {
	conceptsDir := flag.String("concepts-dir", "docs/concepts", "path to concept files")
	outputFile := flag.String("output", "docs/glossary.md", "path to write generated glossary")
	check := flag.Bool("check", false, "verify existing output matches generated; exit non-zero on diff")
	flag.Parse()

	if err := run(*conceptsDir, *outputFile, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(conceptsDir, outputPath string, check bool) error {
	got, err := generate(conceptsDir)
	if err != nil {
		return err
	}
	if check {
		want, err := os.ReadFile(outputPath)
		if err != nil {
			return fmt.Errorf("%s: %w", outputPath, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("%s differs from generator output; run `make docs-glossary` to regenerate", outputPath)
		}
		return nil
	}
	return os.WriteFile(outputPath, got, 0644)
}
