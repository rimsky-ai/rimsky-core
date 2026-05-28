// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"os"
	"path/filepath"
	"testing"

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
	for _, name := range bundledRoleNames() {
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

func TestApplyGrantPatches_AddRemoveDryRun(t *testing.T) {
	base := auth.Grant{{Action: "*:read"}}
	got, err := applyGrantPatches(base, []string{"node:invalidate"}, []string{"*:read"}, []string{"message:send"})
	if err != nil {
		t.Fatalf("applyGrantPatches: %v", err)
	}
	// After: --add added; --remove removed; --dry-run added with mode=dry_run.
	if len(got) != 2 {
		t.Fatalf("expected 2 entries; got %d: %+v", len(got), got)
	}
	if got[0].Action != "node:invalidate" || got[0].Mode != auth.ModeExecute {
		t.Errorf("entry[0]: %+v", got[0])
	}
	if got[1].Action != "message:send" || got[1].Mode != auth.ModeDryRun {
		t.Errorf("entry[1]: %+v", got[1])
	}
}

func TestApplyGrantPatches_DryRunReadRejected(t *testing.T) {
	_, err := applyGrantPatches(nil, nil, nil, []string{"node:read"})
	if err == nil {
		t.Fatalf("expected error for dry-run on read action")
	}
}

func TestApplyGrantPatches_DryRunAuthRejected(t *testing.T) {
	for _, action := range []string{"auth:create", "auth:revoke", "auth:rotate"} {
		_, err := applyGrantPatches(nil, nil, nil, []string{action})
		if err == nil {
			t.Errorf("expected error for dry-run on %q", action)
		}
	}
}
