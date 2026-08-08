// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: testing-scenario-based-e2e
// @decision: test-wallclock-lint-ratchet
package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var goTestTimeoutFlag = regexp.MustCompile(`-timeout[= ]+([^\s]+)`)

func timeoutBearingFiles(root string) []string {
	return []string{
		filepath.Join(root, "Makefile"),
		filepath.Join(root, ".github", "workflows", "ci.yml"),
		filepath.Join(root, ".github", "workflows", "release.yml"),
	}
}

func TestNoAggregateGoTestTimeout(t *testing.T) {
	root := findRepoRoot(t)

	for _, path := range timeoutBearingFiles(root) {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "golangci-lint") {
				continue
			}
			m := goTestTimeoutFlag.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if m[1] == "0" {
				continue
			}
			t.Fatalf("%s:%d declares a non-zero go-test timeout (%q).\n"+
				"A per-package -timeout is an aggregate budget covering every test in the package, so machine load "+
				"decides which arbitrary tests get killed — a load-dependent verdict. Run with -timeout 0 and let "+
				"tools/gotest-guard.sh's no-progress watchdog be the backstop.\n  line: %s",
				path, i+1, m[1], strings.TrimSpace(line))
		}
	}
}
