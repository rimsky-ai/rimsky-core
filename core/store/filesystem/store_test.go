package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fallguy/rimsky/core/store"
)

// TestFactoryBuild_HappyPath validates the canonical config shape.
func TestFactoryBuild_HappyPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := Factory{}.Build("content", map[string]any{"root": root})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.Name() != "content" {
		t.Fatalf("Name() = %q, want %q", s.Name(), "content")
	}
	if s.Kind() != "filesystem" {
		t.Fatalf("Kind() = %q, want %q", s.Kind(), "filesystem")
	}
	if got := s.Capabilities().WriteSemantics; got != store.WriteSemanticsDirect {
		t.Fatalf("WriteSemantics = %q, want direct", got)
	}
}

// TestFactory_MaxWriteSemantics confirms the substrate ceiling is direct.
func TestFactory_MaxWriteSemantics(t *testing.T) {
	t.Parallel()

	if got := (Factory{}).MaxWriteSemantics(); got != store.WriteSemanticsDirect {
		t.Fatalf("MaxWriteSemantics = %q, want direct", got)
	}
}

// TestFactoryBuild_RejectsBadConfig covers the validation branches.
func TestFactoryBuild_RejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{name: "missing root", cfg: map[string]any{}},
		{name: "non-string root", cfg: map[string]any{"root": 1}},
		{name: "empty root", cfg: map[string]any{"root": ""}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := (Factory{}).Build("x", tc.cfg); err == nil {
				t.Fatalf("Build(%v): expected error, got nil", tc.cfg)
			}
		})
	}
}

// TestStore_Open_ReturnsAddressAndRegion exercises the canonical Open
// flow: a glob selector resolves to an address rooted under the store
// root and a region serialised as a JSON glob list.
func TestStore_Open_ReturnsAddressAndRegion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := mustBuild(t, root)

	cr, err := s.Open(context.Background(), store.ClaimSpec{
		StoreName: "content",
		Selector:  "reports/*",
		Intent:    store.IntentReadWrite,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var addr string
	if err := json.Unmarshal(cr.Address, &addr); err != nil {
		t.Fatalf("Address not a JSON string: %v (%q)", err, cr.Address)
	}
	want := filepath.Join(root, "reports")
	if addr != want {
		t.Fatalf("Address path = %q, want %q", addr, want)
	}
	if string(cr.Region) != `["reports/*"]` {
		t.Fatalf("Region = %q, want %q", cr.Region, `["reports/*"]`)
	}
	if cr.Payload != nil {
		t.Fatalf("Payload = %v, want nil for direct mode", cr.Payload)
	}
}

// TestStore_Open_EmptySelector rejects with an error.
func TestStore_Open_EmptySelector(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	if _, err := s.Open(context.Background(), store.ClaimSpec{StoreName: "content"}); err == nil {
		t.Fatalf("Open(empty selector): expected error, got nil")
	}
}

// TestStore_Commit_Abandon_Release_NoOp exercises the no-op verbs.
func TestStore_Commit_Abandon_Release_NoOp(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	ctx := context.Background()
	region := []byte(`["reports/*"]`)
	addr := []byte(`"/data/reports"`)
	if err := s.Commit(ctx, region, addr, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.Abandon(ctx, region, addr, ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := s.Release(ctx, region, addr); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestStore_Delete removes the resolved region from disk.
func TestStore_Delete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "reports", "q1"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(root, "reports", "q1", "summary.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := mustBuild(t, root)
	region, _ := json.Marshal([]string{"reports/q1"})
	if err := s.Delete(context.Background(), region); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file still exists after Delete: err=%v", err)
	}
}

// TestStore_RegionsConflict_DelegatesToHelper sanity-checks the
// bytes-typed wrapper: same-shape input delegates to the pure helper;
// malformed bytes fail-closed (return true).
func TestStore_RegionsConflict_DelegatesToHelper(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())

	if !s.RegionsConflict([]byte(`["a/*"]`), []byte(`["a/foo"]`)) {
		t.Fatalf("expected conflict for overlapping globs")
	}
	if s.RegionsConflict([]byte(`["a/*"]`), []byte(`["b/*"]`)) {
		t.Fatalf("expected disjoint")
	}
	// Malformed bytes fail closed.
	if !s.RegionsConflict([]byte(`not-json`), []byte(`["a/*"]`)) {
		t.Fatalf("expected fail-closed conflict on malformed bytes")
	}
}

// TestStore_UnmarshalRegion verifies the canonical-bytes round-trip and
// rejects malformed input.
func TestStore_UnmarshalRegion(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	got, err := s.UnmarshalRegion([]byte(`["a/*", "b/foo"]`))
	if err != nil {
		t.Fatalf("UnmarshalRegion: %v", err)
	}
	if string(got) != `["a/*", "b/foo"]` {
		t.Fatalf("got %q, want unchanged input", got)
	}
	if _, err := s.UnmarshalRegion([]byte(`{"not": "an array"}`)); err == nil {
		t.Fatalf("expected error on object input")
	}
	if _, err := s.UnmarshalRegion([]byte(`not-json`)); err == nil {
		t.Fatalf("expected error on garbage input")
	}
}

func mustBuild(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Factory{}.Build("content", map[string]any{"root": root})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s.(*Store)
}
