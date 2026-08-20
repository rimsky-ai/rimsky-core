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
			fmt.Printf("%s:%d: %s: %s — %s\n", v.File, v.Line, v.Kind, v.Source, v.Detail)
		}
		fmt.Printf("wallclock-lint: %d violation(s)\n", len(violations))
		return
	}

	if hard := scan.NonBaselineable(violations); len(hard) > 0 {
		for _, v := range hard {
			fmt.Fprintf(os.Stderr, "wallclock-lint: %s:%d: %s: %s — %s\n", v.File, v.Line, v.Kind, v.Source, v.Detail)
		}
		fmt.Fprintln(os.Stderr, "wallclock-lint: a declared wait class the lint refuses cannot be recorded in the baseline — "+
			"the baseline carries the unclassified backlog alone")
		os.Exit(1)
	}

	counts := scan.CountsByFile(violations)
	out := filepath.Join(repoRoot, "tools", "wallclock-lint", "baseline.json")
	previous, err := readBaseline(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wallclock-lint:", err)
		os.Exit(1)
	}
	if grown := grownFiles(previous, counts); len(grown) > 0 {
		for _, g := range grown {
			fmt.Fprintf(os.Stderr, "wallclock-lint: %s has %d unclassified wall-clock wait(s), the recorded baseline allows %d\n",
				g.file, g.now, g.was)
		}
		fmt.Fprintln(os.Stderr, "wallclock-lint: the ratchet is one-way — convert the new wait rather than recording it")
		os.Exit(1)
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

func readBaseline(path string) (map[string]int, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return counts, nil
}

type grownFile struct {
	file string
	was  int
	now  int
}

func grownFiles(previous, current map[string]int) []grownFile {
	var out []grownFile
	for file, now := range current {
		if was := previous[file]; now > was {
			out = append(out, grownFile{file: file, was: was, now: now})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out
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
