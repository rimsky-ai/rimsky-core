// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestClaimScopeCanonicalNamingSymbols(t *testing.T) {
	if got, want := string(persistence.LockKindScope), "claim_scope"; got != want {
		t.Fatalf("persistence.LockKindScope = %q, want %q", got, want)
	}

	field, ok := reflect.TypeOf(persistence.ClaimHandleRow{}).FieldByName("ClaimScopeData")
	if !ok {
		t.Fatalf("persistence.ClaimHandleRow has no ClaimScopeData field")
	}
	jsonTag := field.Tag.Get("json")
	if !strings.HasPrefix(jsonTag, "claim_scope_data") {
		t.Fatalf("ClaimHandleRow.ClaimScopeData json tag = %q, want prefix %q", jsonTag, "claim_scope_data")
	}

	var _ func(a, b []byte) bool = locks.ClaimScopesByteEqual
}

func TestClaimScopeRetiredVocabularyAbsent(t *testing.T) {
	repoRoot := findRepoRoot(t)

	forbidden := []string{
		"region_data",
		"RegionConflict",
		`"scope_data"`,
		"func ScopesByteEqual(",
		"lock_kind = 'scope'",
		"lock_kind IN ('named','scope')",
	}

	nonTestOnlyForbidden := []string{
		".scope}}",
	}

	allowedFiles := map[string]bool{
		filepath.Join(repoRoot, "test", "plumbline", "claim_scope_naming_test.go"): true,
	}

	scanRepoForForbiddenVocabulary(t, repoRoot, forbidden, nonTestOnlyForbidden, allowedFiles,
		"retired claim-scope vocabulary %q found in %s; the concept is named ClaimScope everywhere "+
			"(claim_scope_data, lock_kind claim_scope, ClaimScopesByteEqual) per "+
			".ok-planner/design/intent/claim-scope.md")
}

func scanRepoForForbiddenVocabulary(
	t *testing.T, repoRoot string,
	forbidden, nonTestOnlyForbidden []string,
	allowedFiles map[string]bool,
	msgFormat string,
) {
	t.Helper()

	skipDirs := map[string]bool{
		".git":         true,
		".ok-planner":  true,
		"node_modules": true,
		"bin":          true,
		"tmp":          true,
	}

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
		if allowedFiles[path] {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" && ext != ".proto" && ext != ".yaml" && ext != ".yml" {
			return nil
		}

		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		content := string(raw)

		terms := forbidden
		if !strings.HasSuffix(path, "_test.go") {
			terms = append(append([]string{}, forbidden...), nonTestOnlyForbidden...)
		}
		for _, term := range terms {
			if strings.Contains(content, term) {
				relPath, _ := filepath.Rel(repoRoot, path)
				t.Errorf(msgFormat, term, relPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoRoot, err)
	}
}
