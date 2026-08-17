// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var makefileRecipeLine = regexp.MustCompile(`^\t.*GOTEST_GUARD.*$`)

func makefileRecipe(t *testing.T, src, target string) []string {
	t.Helper()
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if line != target+":" {
			continue
		}
		var recipe []string
		for _, r := range lines[i+1:] {
			if !strings.HasPrefix(r, "\t") {
				break
			}
			recipe = append(recipe, r)
		}
		if len(recipe) == 0 {
			t.Fatalf("Makefile target %q has an empty recipe", target)
		}
		return recipe
	}
	t.Fatalf("Makefile target %q not found", target)
	return nil
}

func recipeCarriesParallelCap(recipe []string) bool {
	for _, line := range recipe {
		if !makefileRecipeLine.MatchString(line) {
			continue
		}
		if strings.Contains(line, "-p 2") && strings.Contains(line, "-parallel 4") {
			return true
		}
	}
	return false
}

// @decision: parallel-cap-removal
// @decision: config-enforced-fitness-tests
func TestMakefileTestRecipesCapOnlyTheDockerBackedSuites(t *testing.T) {
	path := filepath.Join(findRepoRoot(t), "Makefile")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	dockerBacked := []string{"test-root", "test-foundation", "test-services"}
	for _, target := range dockerBacked {
		if !recipeCarriesParallelCap(makefileRecipe(t, src, target)) {
			t.Errorf("Makefile target %q runs testcontainers-backed suites against the one docker daemon "+
				"and must carry the saturation cap (-p 2 -parallel 4)", target)
		}
	}

	uncapped := []string{"test-protocols"}
	for _, target := range uncapped {
		if recipeCarriesParallelCap(makefileRecipe(t, src, target)) {
			t.Errorf("Makefile target %q boots no containers, so a parallelism cap there is scheduling "+
				"insurance rather than a bound on a named contention", target)
		}
	}
	t.Logf("checked %d capped and %d uncapped module test recipes", len(dockerBacked), len(uncapped))
}
