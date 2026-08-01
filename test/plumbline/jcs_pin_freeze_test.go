// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// @decision: jcs-cyberphone
// @concept: template
func TestJCSCanonicalizationPinIsFrozen(t *testing.T) {
	const modulePath = "github.com/cyberphone/json-canonicalization"
	const frozenPin = modulePath + " v0.0.0-20241213102144-19d51d7fe467"

	repoRoot := findRepoRoot(t)
	manifests := workspaceManifests(t, repoRoot)

	requireLine := regexp.MustCompile(regexp.QuoteMeta(modulePath) + ` v[^\s]+`)

	required := 0
	for _, rel := range manifests {
		manifest, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := string(manifest)
		if replacesModule(content, modulePath) {
			t.Errorf("%s carries a replace directive for %s — a replace bypasses the frozen pin and "+
				"silently substitutes different canonicalization output bytes; remove it", rel, modulePath)
		}
		got := requireLine.FindString(content)
		if got == "" {
			continue
		}
		required++
		if got != frozenPin {
			t.Errorf("the JCS canonicalization pin moved in %s: got %q, frozen at %q.\n"+
				"The pin is permanent: its exact output bytes are load-bearing for template identity — "+
				"a bump that changes canonicalization output silently splits template identity between "+
				"persisted ids and newly computed ones. Moving it is a deliberate act that must ship "+
				"together with a decision on template-identity migration (update decision:jcs-cyberphone "+
				"and this frozen constant in the same change).", rel, got, frozenPin)
		}
	}
	if required == 0 {
		t.Fatal("no workspace manifest requires the JCS canonicalization library — template ids are hashes over its exact output bytes")
	}
}

func workspaceManifests(t *testing.T, repoRoot string) []string {
	work, err := os.ReadFile(filepath.Join(repoRoot, "go.work"))
	if err != nil {
		t.Fatalf("read go.work: %v", err)
	}
	useDir := regexp.MustCompile(`(?m)^\s*(?:use\s+)?(\.[^\s()]*)\s*$`)
	var manifests []string
	for _, m := range useDir.FindAllStringSubmatch(string(work), -1) {
		manifests = append(manifests, filepath.Join(m[1], "go.mod"))
	}
	if len(manifests) == 0 {
		t.Fatal("go.work lists no module directories")
	}
	return manifests
}

func replacesModule(content, modulePath string) bool {
	if regexp.MustCompile(`(?m)^\s*replace\s+` + regexp.QuoteMeta(modulePath) + `\b`).MatchString(content) {
		return true
	}
	blockRe := regexp.MustCompile(`(?m)^\s*replace\s*\(([^)]*)\)`)
	for _, m := range blockRe.FindAllStringSubmatch(content, -1) {
		if regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(modulePath) + `\b`).MatchString(m[1]) {
			return true
		}
	}
	return false
}
