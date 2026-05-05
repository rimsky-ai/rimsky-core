// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestLLMSTxtValidity_GoodPasses(t *testing.T) {
	err := runLLMSTxtValidity([]string{
		"-llms-txt=testdata/llms-good/docs/agents/llms.txt",
		"-llms-full=testdata/llms-good/docs/agents/llms-full.txt",
		"-repo-root=testdata/llms-good",
		"-root-llms-txt=testdata/llms-good/llms.txt",
		"-root-llms-full=testdata/llms-good/llms-full.txt",
	})
	if err != nil {
		t.Errorf("expected pass, got %v", err)
	}
}

func TestLLMSTxtValidity_BrokenLinkFails(t *testing.T) {
	err := runLLMSTxtValidity([]string{
		"-llms-txt=testdata/llms-bad-broken-link/docs/agents/llms.txt",
		"-llms-full=testdata/llms-bad-broken-link/docs/agents/llms-full.txt",
		"-repo-root=testdata/llms-bad-broken-link",
		"-root-llms-txt=testdata/llms-bad-broken-link/llms.txt",
		"-root-llms-full=testdata/llms-bad-broken-link/llms-full.txt",
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "does-not-exist.md") {
		t.Errorf("expected broken link in error, got %v", err)
	}
}
