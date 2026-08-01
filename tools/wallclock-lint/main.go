// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: test-wallclock-lint-ratchet

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rimsky-ai/rimsky-core/tools/wallclock-lint/scan"
)

func main() {
	list := flag.Bool("list", false, "print every violation instead of writing the baseline")
	grow := flag.Bool("grow", false, "allow the baseline to record an increased per-file count (the ratchet is one-way without this)")
	flag.Parse()

	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wallclock-lint:", err)
		os.Exit(1)
	}
	violations, err := scan.TestCodeViolations(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wallclock-lint:", err)
		os.Exit(1)
	}
	if *list {
		for _, v := range violations {
			fmt.Printf("%s:%d: %s: %s\n", v.File, v.Line, v.Detector, v.Source)
		}
		fmt.Printf("wallclock-lint: %d violation(s)\n", len(violations))
		return
	}
	counts := scan.CountsByFile(violations)
	out := filepath.Join(repoRoot, "tools", "wallclock-lint", "baseline.json")
	if !*grow {
		baseline := readBaseline(out)
		var grown []string
		for file, n := range counts {
			if n > baseline[file] {
				grown = append(grown, file)
			}
		}
		if len(grown) > 0 {
			sort.Strings(grown)
			for _, f := range grown {
				fmt.Fprintf(os.Stderr, "wallclock-lint: %s would grow past its recorded baseline (%d -> %d)\n", f, baseline[f], counts[f])
			}
			fmt.Fprintln(os.Stderr, "wallclock-lint: refusing to record an increased per-file count — fix the new "+
				"wall-clock verdict idiom(s) or rerun with -grow to deliberately raise the baseline")
			os.Exit(1)
		}
	}
	blob, err := json.MarshalIndent(counts, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "wallclock-lint:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, append(blob, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "wallclock-lint:", err)
		os.Exit(1)
	}
	fmt.Printf("wallclock-lint: %d violation(s) across %d file(s) -> %s\n", len(violations), len(counts), out)
}

func readBaseline(path string) map[string]int {
	baseline := map[string]int{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return baseline
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return map[string]int{}
	}
	return baseline
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.work not found above %s", dir)
		}
		dir = parent
	}
}
