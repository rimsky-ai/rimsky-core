// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var numberedInvariantPattern = regexp.MustCompile(`(?i)\b(invariant|inv)s?\b[\s#-]*\d+`)

func TestNoNumberedInvariantReferences(t *testing.T) {
	repoRoot := findRepoRoot(t)

	skipDirs := map[string]bool{
		".git":         true,
		".ok-planner":  true,
		"node_modules": true,
		"bin":          true,
		"tmp":          true,
	}

	selfPath := filepath.Join(repoRoot, "test", "plumbline", "numbered_invariant_test.go")

	var violations []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || path == selfPath {
			return nil
		}

		file, ferr := os.Open(path)
		if ferr != nil {
			return ferr
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if numberedInvariantPattern.MatchString(line) {
				relPath, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					relPath = path
				}
				violations = append(violations, fmt.Sprintf("%s:%d: %s", relPath, lineNum, strings.TrimSpace(line)))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}

	if len(violations) > 0 {
		t.Fatalf("numbered-invariant references found in source (concept-doc invariant numbering is design-doc-internal "+
			"and must not be cited from code, error strings, or test names/messages; name the behavior in words instead):\n%s",
			strings.Join(violations, "\n"))
	}
}
