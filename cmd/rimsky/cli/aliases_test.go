// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServiceAliases_GlobalOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, t.TempDir())
	writeAliasFile(t, filepath.Join(home, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: /opt/codegen\n  fs: /opt/fs\n")

	got := LoadServiceAliases()
	if got["codegen"] != "/opt/codegen" || got["fs"] != "/opt/fs" {
		t.Fatalf("global aliases not loaded: %+v", got)
	}
}

func TestLoadServiceAliases_ProjectOverlaysGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAliasFile(t, filepath.Join(home, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: /opt/global-cg\n  shared: /opt/shared\n")

	proj := t.TempDir()
	chdir(t, proj)
	writeAliasFile(t, filepath.Join(proj, ".rimsky", "aliases.yml"),
		"aliases:\n  codegen: /opt/local-cg\n  only-local: /opt/local\n")

	got := LoadServiceAliases()
	if got["codegen"] != "/opt/local-cg" {
		t.Fatalf("project-local should override global for codegen: %+v", got)
	}
	if got["shared"] != "/opt/shared" {
		t.Fatalf("global-only entry missing: %+v", got)
	}
	if got["only-local"] != "/opt/local" {
		t.Fatalf("project-local-only entry missing: %+v", got)
	}
}

func TestLoadServiceAliases_MissingFilesAreFine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdir(t, t.TempDir())
	got := LoadServiceAliases()
	if len(got) != 0 {
		t.Fatalf("expected empty map with no alias files, got %+v", got)
	}
}

func TestLoadServiceAliases_MalformedFileSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".rimsky"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".rimsky", "aliases.yml"), []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadServiceAliases()
	if len(got) != 0 {
		t.Fatalf("malformed alias file should be skipped, got %+v", got)
	}
}
