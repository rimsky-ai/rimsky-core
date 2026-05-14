// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestVocabulary_CleanPasses(t *testing.T) {
	err := runVocabulary([]string{
		"-config=testdata/vocabulary-config/.vocabulary-lint.yml",
		"-repo-root=testdata/vocabulary-good",
	})
	if err != nil {
		t.Errorf("expected pass, got %v", err)
	}
}

func TestVocabulary_DirtyFails(t *testing.T) {
	err := runVocabulary([]string{
		"-config=testdata/vocabulary-config/.vocabulary-lint.yml",
		"-repo-root=testdata/vocabulary-bad",
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "template_id") || !strings.Contains(msg, "instance_key") || !strings.Contains(msg, "substrate") {
		t.Errorf("expected all three terms flagged, got %s", msg)
	}
}

// TestVocabulary_FrontmatterSkipped exercises the lint's frontmatter-skip
// behavior on .md files. Concept files declare deprecated vocabulary
// inside the YAML frontmatter's `deprecated_terms:` list — the official
// declaration site. The lint scanner must not flag those entries.
func TestVocabulary_FrontmatterSkipped(t *testing.T) {
	err := runVocabulary([]string{
		"-config=testdata/vocabulary-config/.vocabulary-lint.yml",
		"-repo-root=testdata/vocabulary-frontmatter-skip",
	})
	if err != nil {
		t.Errorf("expected pass (deprecated terms in frontmatter must be skipped), got %v", err)
	}
}
