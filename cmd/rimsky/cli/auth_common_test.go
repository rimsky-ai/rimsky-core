// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/roles"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func TestLoadRole_Bundled(t *testing.T) {
	r, err := loadRole("admin", "")
	if err != nil {
		t.Fatalf("loadRole(admin): %v", err)
	}
	if r.Name != "admin" {
		t.Fatalf("admin role name: %q", r.Name)
	}
	if len(r.Permissions) == 0 {
		t.Fatalf("admin role has no permissions")
	}
}

func TestLoadRole_AllBundled(t *testing.T) {
	for _, name := range roles.AllNames() {
		r, err := loadRole(name, "")
		if err != nil {
			t.Errorf("loadRole(%s): %v", name, err)
		}
		if r.Name == "" || len(r.Permissions) == 0 {
			t.Errorf("loadRole(%s): empty fields", name)
		}
	}
}

func TestLoadRole_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(path, []byte(`{"name":"custom","description":"x","permissions":[{"action":"node:read"}]}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := loadRole("", path)
	if err != nil {
		t.Fatalf("loadRole file: %v", err)
	}
	if r.Name != "custom" || len(r.Permissions) != 1 {
		t.Fatalf("custom role: %+v", r)
	}
}

func TestApplyGrantPatches_AddRemove(t *testing.T) {
	base := auth.Grant{{Action: "*:read"}}
	got, err := applyGrantPatches(base, []string{"node:reset", "message:send"}, []string{"*:read"})
	if err != nil {
		t.Fatalf("applyGrantPatches: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries; got %d: %+v", len(got), got)
	}
	if got[0].Action != "node:reset" {
		t.Errorf("entry[0]: %+v", got[0])
	}
	if got[1].Action != "message:send" {
		t.Errorf("entry[1]: %+v", got[1])
	}
}

func TestApplyGrantPatches_BadAddRejected(t *testing.T) {
	if _, err := applyGrantPatches(nil, []string{"no-colon"}, nil); err == nil {
		t.Fatalf("expected error for --add of a malformed action string")
	}
}
