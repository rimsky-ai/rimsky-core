// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: test-wallclock-lint-ratchet

package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const SuppressionMarker = "//nolint:testwallclock"

var detectors = []struct {
	Name    string
	Pattern *regexp.Regexp
}{
	{"eventually", regexp.MustCompile(`\b(require|assert)\.(Eventually|EventuallyWithT|Eventuallyf|EventuallyWithTf|Never|Neverf)\(`)},
	{"timeout-select", regexp.MustCompile(`case\s+<-time\.After\(`)},
	{"deadline-poll", regexp.MustCompile(`for\s+time\.Now\(\)\.Before\(`)},
	{"deadline-poll", regexp.MustCompile(`for\s+time\.Since\(`)},
}

var scanRoots = []string{"cmd", "lib", "test", "tools", "examples"}

func isTestCode(rel string) bool {
	if !strings.HasSuffix(rel, ".go") {
		return false
	}
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	if strings.HasPrefix(filepath.ToSlash(rel), "lib/foundation/persistence/conformance/") {
		return true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts[:len(parts)-1] {
		if p == "test" || p == "testfixture" || p == "storetest" || p == "testdata" {
			return true
		}
	}
	return false
}

func suppressionWithJustification(line string) bool {
	idx := strings.Index(line, SuppressionMarker)
	if idx < 0 {
		return false
	}
	return strings.TrimSpace(line[idx+len(SuppressionMarker):]) != ""
}

type Violation struct {
	File     string
	Line     int
	Detector string
	Source   string
}

func TestCodeViolations(repoRoot string) ([]Violation, error) {
	var out []Violation
	for _, root := range scanRoots {
		rootPath := filepath.Join(repoRoot, root)
		if _, err := os.Stat(rootPath); err != nil {
			continue
		}
		err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "gen" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if !isTestCode(rel) {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			lines := strings.Split(string(src), "\n")
			for i, line := range lines {
				if suppressionWithJustification(line) {
					continue
				}
				if i > 0 && suppressionWithJustification(lines[i-1]) {
					continue
				}
				for _, det := range detectors {
					if det.Pattern.MatchString(line) {
						out = append(out, Violation{File: rel, Line: i + 1, Detector: det.Name, Source: strings.TrimSpace(line)})
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func CountsByFile(violations []Violation) map[string]int {
	counts := map[string]int{}
	for _, v := range violations {
		counts[v.File]++
	}
	return counts
}
