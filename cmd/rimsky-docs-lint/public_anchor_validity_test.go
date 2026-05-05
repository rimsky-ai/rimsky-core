// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestPublicAnchorValidity_GoodPasses(t *testing.T) {
	err := runPublicAnchorValidity([]string{
		"-concepts-dir=testdata/anchor-good/concepts",
		"-proto-dir=testdata/anchor-good/proto",
	})
	if err != nil {
		t.Errorf("expected pass, got %v", err)
	}
}

func TestPublicAnchorValidity_BadFails(t *testing.T) {
	err := runPublicAnchorValidity([]string{
		"-concepts-dir=testdata/anchor-bad/concepts",
		"-proto-dir=testdata/anchor-bad/proto",
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "NonexistentMsg") {
		t.Errorf("expected NonexistentMsg in error, got %v", err)
	}
}
