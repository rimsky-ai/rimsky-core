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

var executorEnvPattern = regexp.MustCompile(`"(RIMSKY_[A-Z0-9_]+)"`)

var genericExecutorEnv = map[string]bool{
	"RIMSKY_EXECUTOR_HOST":                  true,
	"RIMSKY_EXECUTOR_PORT_GRPC":             true,
	"RIMSKY_EXECUTOR_PORT_HTTP":             true,
	"RIMSKY_AGENT_PORT":                     true,
	"RIMSKY_EXECUTOR_STUB_MODE":             true,
	"RIMSKY_EXECUTOR_CLAUDE_BINARY":         true,
	"RIMSKY_EXECUTOR_SILENCE_MS":            true,
	"RIMSKY_EXECUTOR_TOOL_USE_TIMEOUT_MS":   true,
	"RIMSKY_EXECUTOR_DECLARED_TAGS":         true,
	"RIMSKY_CALLBACK_URL":                   true,
	"RIMSKY_CALLBACK_TOKEN":                 true,
	"RIMSKY_PEER_AUTH":                      true,
	"RIMSKY_LOG_LEVEL":                      true,
	"RIMSKY_LOG_BINARY":                     true,
	"RIMSKY_CONTROL_API_URL":                true,
	"RIMSKY_CONTROL_API_TOKEN":              true,
	"RIMSKY_CONTROL_API_CA":                 true,
	"RIMSKY_OBSERVABILITY_REFRESH_INTERVAL": true,
}

func serviceEnvSegment(dir string) string {
	return strings.ToUpper(strings.ReplaceAll(dir, "-", "_"))
}

// @decision: operator-env-namespaced-per-service
func TestBundledExecutorOperatorEnvNamesCarryTheirServiceSegment(t *testing.T) {
	root := filepath.Join(findRepoRoot(t), "lib", "services", "executors")
	services, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	checked := 0
	for _, svc := range services {
		if !svc.IsDir() {
			continue
		}
		segment := serviceEnvSegment(svc.Name())
		svcRoot := filepath.Join(root, svc.Name())
		walkErr := filepath.WalkDir(svcRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for _, m := range executorEnvPattern.FindAllStringSubmatch(string(src), -1) {
				name := m[1]
				if genericExecutorEnv[name] {
					continue
				}
				checked++
				if !strings.Contains(name, segment) {
					t.Errorf("%s reads %s: an operator variable specific to the %s executor carries its service segment %s — "+
						"the generic per-executor variables (listen host and ports, binary override, declared tags, timeouts, stub mode) "+
						"are the only unprefixed names", path, name, svc.Name(), segment)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", svcRoot, walkErr)
		}
	}
	if checked == 0 {
		t.Fatalf("no service-specific operator variables found under %s: the check inspected nothing", root)
	}
	t.Logf("checked %d service-specific operator variable reads", checked)
}
