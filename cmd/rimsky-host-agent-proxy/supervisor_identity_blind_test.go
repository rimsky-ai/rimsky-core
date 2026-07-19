// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @concept: host-agent-proxy
// @concept: orphan-reaper
func TestProxySourceNeverReferencesRimskySupervisorIdentity(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		checked++
		lower := strings.ToLower(string(b))
		if strings.Contains(lower, "supervisor") {
			t.Fatalf("%s references \"supervisor\": the proxy must route purely by instance/owner identity and stay blind to which rimsky supervisor dispatched the call (so a reaper-driven reclaim by a different supervisor is invisible to it)", name)
		}
	}
	if checked == 0 {
		t.Fatalf("no source files found in the proxy package; the scan is broken")
	}
}
