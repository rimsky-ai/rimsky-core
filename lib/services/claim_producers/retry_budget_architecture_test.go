// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducers_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var retryBudgetVocabulary = regexp.MustCompile(`(?i)retry[_]?budget|retry[_]?count|max[_]?retries|attempt[_]?budget|error[_]?budget|retrycounter`)

func TestClaimProducerStores_DoNotImplementRetryBudget(t *testing.T) {
	roots := []string{
		"postgres/store",
		"postgres/server",
		"filesystem/store",
		"filesystem/server",
	}
	for _, root := range roots {
		root := root
		t.Run(root, func(t *testing.T) {
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatalf("read dir %q: %v", root, err)
			}
			found := 0
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				path := filepath.Join(root, entry.Name())
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %q: %v", path, err)
				}
				found += len(retryBudgetVocabulary.FindAll(contents, -1))
			}
			if found > 0 {
				t.Fatalf("%s contains retry-budget vocabulary (%v); retry budget is node-level policy "+
					"(lib/graph/node/template_validator.go + lib/runtime/runner_error_policy.go) and must never "+
					"be re-implemented inside a claim-producer store", root, retryBudgetVocabulary)
			}
		})
	}
}
