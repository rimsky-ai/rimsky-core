// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var transportEnvPattern = regexp.MustCompile(`"(RIMSKY_[A-Z0-9_]*(?:HOST|PORT)[A-Z0-9_]*)"`)

var genericTransportEnv = map[string]bool{
	"RIMSKY_EXECUTOR_HOST":      true,
	"RIMSKY_EXECUTOR_PORT_GRPC": true,
	"RIMSKY_EXECUTOR_PORT_HTTP": true,
	"RIMSKY_AGENT_PORT":         true,
}

// @decision: operator-env-namespaced-per-service
func TestBundledExecutorsShareUnprefixedTransportEnvNames(t *testing.T) {
	root := filepath.Join(findRepoRoot(t), "lib", "services", "executors")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range transportEnvPattern.FindAllStringSubmatch(string(src), -1) {
			if !genericTransportEnv[m[1]] {
				t.Errorf("%s reads %s: the generic per-executor listen host and port variables stay unprefixed and identical across the bundled executors — per-service prefixing is for behavior-specific knobs", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
