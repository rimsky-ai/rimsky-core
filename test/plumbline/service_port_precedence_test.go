// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var shippedServiceFamilies = []string{"claim_producers", "executors", "sensors", "subscribers"}

func serviceTreeSources(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		b.Write(src)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return b.String()
}

// @concept: service
// @concept: host-agent
func TestEveryListeningBundledServiceResolvesItsPortThroughTheSharedPrecedence(t *testing.T) {
	servicesRoot := filepath.Join(findRepoRoot(t), "lib", "services")

	var listening, portless []string
	for _, family := range shippedServiceFamilies {
		familyRoot := filepath.Join(servicesRoot, family)
		entries, err := os.ReadDir(familyRoot)
		if err != nil {
			t.Fatalf("read %s: %v", familyRoot, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			svcRoot := filepath.Join(familyRoot, e.Name())
			src := serviceTreeSources(t, svcRoot)
			if !strings.Contains(src, "func main()") {
				continue
			}
			name := family + "/" + e.Name()
			if !strings.Contains(src, "serverkit.Listen") && !strings.Contains(src, "net.Listen") {
				portless = append(portless, name)
				continue
			}
			listening = append(listening, name)
			if !strings.Contains(src, "agentport.") {
				t.Errorf("%s serves a port but never calls agentport: every bundled service binary that listens "+
					"resolves its serving port through the shared precedence — the agent-assigned port variable "+
					"first, then its own port variable, then the built-in default — so the host agent can late-bind it",
					name)
			}
		}
	}
	sort.Strings(listening)
	sort.Strings(portless)
	if len(listening) == 0 {
		t.Fatalf("no listening bundled service binaries found under %s: the check inspected nothing", servicesRoot)
	}
	t.Logf("checked %d listening service binaries: %s", len(listening), strings.Join(listening, ", "))
	t.Logf("%d bundled service binaries serve no port and are outside the invariant's population: %s",
		len(portless), strings.Join(portless, ", "))
}
