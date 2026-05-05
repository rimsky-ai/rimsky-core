// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestFrontmatter_GoodFixturePasses(t *testing.T) {
	if err := runFrontmatter([]string{"-dir=testdata/frontmatter-good"}); err != nil {
		t.Errorf("expected pass, got %v", err)
	}
}

func TestFrontmatter_MissingFieldFails(t *testing.T) {
	err := runFrontmatter([]string{"-dir=testdata/frontmatter-bad-missing-field"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "proto_symbol") {
		t.Errorf("expected proto_symbol in error, got %v", err)
	}
}

func TestFrontmatter_FilenameMismatchFails(t *testing.T) {
	err := runFrontmatter([]string{"-dir=testdata/frontmatter-bad-name-mismatch"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "does not match filename") {
		t.Errorf("expected filename-mismatch error, got %v", err)
	}
}
