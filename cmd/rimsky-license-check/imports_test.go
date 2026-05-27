// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeImportsTestConfig builds a small licensingConfig with a foundation
// (AGPL) and protocols (Apache) split, used by the import-direction tests.
func makeImportsTestConfig() *licensingConfig {
	return &licensingConfig{
		apachePrefixes: normalizePrefixes([]string{"protocols/", "foundation/locks/"}),
		agplPrefixes:   normalizePrefixes([]string{"foundation/"}),
	}
}

func writeGoFile(t *testing.T, dir, name, body string) fileEntry {
	t.Helper()
	abs := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return fileEntry{relPath: name, absPath: abs, kind: kindGo}
}

func TestVerifyImportsApacheImportingAGPLFails(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import (
	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
)

var _ = cascade.Sentinel
`
	f := writeGoFile(t, dir, "f.go", src)
	f.classification = classApache
	v := verifyImports([]fileEntry{f}, makeImportsTestConfig())
	if len(v) != 1 {
		t.Fatalf("want 1 violation, got %d (%v)", len(v), v)
	}
}

func TestVerifyImportsAGPLImportingApacheOK(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"

var _ = gen.Sentinel
`
	f := writeGoFile(t, dir, "f.go", src)
	f.classification = classAGPL
	if v := verifyImports([]fileEntry{f}, makeImportsTestConfig()); len(v) != 0 {
		t.Errorf("AGPL importing Apache should be ok, got %v", v)
	}
}

func TestVerifyImportsApacheImportingApacheOK(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import "github.com/rimsky-ai/rimsky-core/foundation/locks"

var _ = locks.Sentinel
`
	f := writeGoFile(t, dir, "f.go", src)
	f.classification = classApache
	if v := verifyImports([]fileEntry{f}, makeImportsTestConfig()); len(v) != 0 {
		t.Errorf("Apache importing Apache should be ok, got %v", v)
	}
}

func TestVerifyImportsMultiLineImportBlock(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import (
	"context"

	// some comment
	"github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/foundation/cascade" // trailing comment
)

var _ = context.Background
var _ = gen.Sentinel
var _ = cascade.Sentinel
`
	f := writeGoFile(t, dir, "f.go", src)
	f.classification = classApache
	v := verifyImports([]fileEntry{f}, makeImportsTestConfig())
	if len(v) != 1 {
		t.Fatalf("want 1 violation for foundation/cascade, got %d (%v)", len(v), v)
	}
}

func TestImportToRepoPath(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"github.com/rimsky-ai/rimsky-core/foundation", "foundation", true},
		{"github.com/rimsky-ai/rimsky-core/foundation/cascade", "foundation/cascade", true},
		{"github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen", "protocols/proto/v1/gen", true},
		{"github.com/rimsky-ai/rimsky-core/cmd/rimsky", "cmd/rimsky", true},
		{"github.com/rimsky-ai/rimsky-core", "", true},
		{"github.com/other/proj", "", false},
	}
	for _, tc := range cases {
		got, ok := importToRepoPath(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("importToRepoPath(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
