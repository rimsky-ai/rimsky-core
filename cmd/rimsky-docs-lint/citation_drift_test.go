// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestCitationDrift_GoodPasses(t *testing.T) {
	err := runCitationDrift([]string{
		"-scope=testdata/citation-good/protocols",
		"-concepts-dir=testdata/citation-good/concepts",
	})
	if err != nil {
		t.Errorf("expected pass, got %v", err)
	}
}

func TestCitationDrift_DriftFails(t *testing.T) {
	err := runCitationDrift([]string{
		"-scope=testdata/citation-bad/protocols",
		"-concepts-dir=testdata/citation-bad/concepts",
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "drift") {
		t.Errorf("expected drift in error, got %v", err)
	}
}

func TestCitationDrift_MissingBlockquoteFails(t *testing.T) {
	err := runCitationDrift([]string{
		"-scope=testdata/citation-no-blockquote/protocols",
		"-concepts-dir=testdata/citation-no-blockquote/concepts",
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "blockquote") {
		t.Errorf("expected blockquote in error, got %v", err)
	}
}
