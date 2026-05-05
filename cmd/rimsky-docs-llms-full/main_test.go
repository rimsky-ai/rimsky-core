// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"strings"
	"testing"
)

func TestGenerate_OrdersAlphabeticallyAndStripsFrontmatter(t *testing.T) {
	got, err := generate("testdata/concepts", "testdata/protocols")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "---\nconcept: alpha") {
		t.Error("frontmatter not stripped from alpha")
	}
	if !strings.Contains(s, "Body for alpha.") || !strings.Contains(s, "Body for beta.") {
		t.Error("expected both body texts in output")
	}
	if !strings.Contains(s, "Gamma protocol") {
		t.Error("expected protocols body")
	}
	iAlpha := strings.Index(s, "Body for alpha.")
	iBeta := strings.Index(s, "Body for beta.")
	if iAlpha < 0 || iBeta < 0 || iAlpha > iBeta {
		t.Errorf("alpha must precede beta; iAlpha=%d iBeta=%d", iAlpha, iBeta)
	}
}
