// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func mkfile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func containsRel(files []fileEntry, rel string) bool {
	for _, f := range files {
		if f.relPath == rel {
			return true
		}
	}
	return false
}

func TestWalkerSkipsVCSDirsHardcoded(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "licensing.yml", "apache:\n  - cmd/\nagpl: []\nexempt: []\n")
	mkfile(t, root, "cmd/main.go", "package main\n")
	mkfile(t, root, ".git/config", "ignored")
	mkfile(t, root, ".svn/entries", "ignored")
	mkfile(t, root, ".hg/store", "ignored")

	cfg, err := loadLicensingYAML(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := walkSourceFiles(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if hasPrefix(f.relPath, ".git/") || hasPrefix(f.relPath, ".svn/") || hasPrefix(f.relPath, ".hg/") {
			t.Errorf("VCS dir leaked into walk: %s", f.relPath)
		}
	}
}

func TestWalkerHonorsExemptList(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "licensing.yml", `apache:
  - cmd/
agpl: []
exempt:
  - cmd/skip-this/
  - protocols/proto/v1/gen/
  - bin/
`)
	mkfile(t, root, "cmd/main.go", "package main\n")
	mkfile(t, root, "cmd/skip-this/x.go", "package main\n")
	mkfile(t, root, "protocols/proto/v1/gen/g.go", "package gen\n")
	mkfile(t, root, "bin/something.go", "package main\n")

	cfg, err := loadLicensingYAML(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := walkSourceFiles(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRel(files, "cmd/main.go") {
		t.Errorf("cmd/main.go should be walked")
	}
	for _, skip := range []string{"cmd/skip-this/x.go", "protocols/proto/v1/gen/g.go", "bin/something.go"} {
		if containsRel(files, skip) {
			t.Errorf("exempt path leaked: %s", skip)
		}
	}
}

func TestWalkerNoHardcodedNodeModulesSkip(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "licensing.yml", "apache:\n  - cmd/\nagpl: []\nexempt: []\n")
	mkfile(t, root, "cmd/main.go", "package main\n")
	mkfile(t, root, "stuff/node_modules/dep/x.go", "package x\n")

	cfg, err := loadLicensingYAML(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := walkSourceFiles(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.relPath == "stuff/node_modules/dep/x.go" && f.classification == classUnknown {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stuff/node_modules/dep/x.go to surface as classUnknown; got files: %v", files)
	}
}

func TestWalkerNodeModulesSkipsWhenExempt(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "licensing.yml", `apache:
  - cmd/
agpl: []
exempt:
  - stuff/node_modules/
`)
	mkfile(t, root, "cmd/main.go", "package main\n")
	mkfile(t, root, "stuff/node_modules/dep/x.go", "package x\n")

	cfg, err := loadLicensingYAML(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := walkSourceFiles(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if containsRel(files, "stuff/node_modules/dep/x.go") {
		t.Errorf("exempt-listed node_modules path should not be walked")
	}
}

func TestSourceKindFor(t *testing.T) {
	cases := []struct {
		name string
		kind sourceKind
		ok   bool
	}{
		{"foo.go", kindGo, true},
		{"foo.ts", kindTS, true},
		{"foo.tsx", kindTS, true},
		{"foo.proto", kindProto, true},
		{"foo.sql", kindSQL, true},
		{"foo.sh", kindShell, true},
		{"README.md", 0, false},
		{"x.txt", 0, false},
		{"Dockerfile", 0, false},
		{"Makefile", 0, false},
	}
	for _, tc := range cases {
		got, ok := sourceKindFor(tc.name)
		if ok != tc.ok || got != tc.kind {
			t.Errorf("sourceKindFor(%q) = (%v, %v), want (%v, %v)", tc.name, got, ok, tc.kind, tc.ok)
		}
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
