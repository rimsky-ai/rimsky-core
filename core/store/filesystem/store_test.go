package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fallguy/rimsky/core/store"
)

// TestFactoryBuild_HappyPath validates the canonical config shape from
// spec §14.1.
func TestFactoryBuild_HappyPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := map[string]any{"mode": "direct", "root": root}
	s, err := Factory{}.Build("content", cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.Name() != "content" {
		t.Fatalf("Name() = %q, want %q", s.Name(), "content")
	}
	if s.Kind() != "filesystem" {
		t.Fatalf("Kind() = %q, want %q", s.Kind(), "filesystem")
	}
	caps := s.Capabilities()
	if !caps.SupportsRegionLock {
		t.Fatalf("SupportsRegionLock = false, want true")
	}
	if !caps.SupportsResume {
		t.Fatalf("SupportsResume = false, want true")
	}
	if caps.SupportsClaim {
		t.Fatalf("SupportsClaim = true, want false")
	}
	if caps.SupportsDiscard {
		t.Fatalf("SupportsDiscard = true, want false")
	}
	if caps.SupportsRestore {
		t.Fatalf("SupportsRestore = true, want false")
	}
}

// TestFactoryBuild_RejectsBadConfig covers the validation branches.
func TestFactoryBuild_RejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{name: "missing mode", cfg: map[string]any{"root": "/tmp/foo"}},
		{name: "non-string mode", cfg: map[string]any{"mode": 1, "root": "/tmp/foo"}},
		{name: "wrong mode", cfg: map[string]any{"mode": "sidecar", "root": "/tmp/foo"}},
		{name: "missing root", cfg: map[string]any{"mode": "direct"}},
		{name: "non-string root", cfg: map[string]any{"mode": "direct", "root": 1}},
		{name: "empty root", cfg: map[string]any{"mode": "direct", "root": ""}},
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

// TestStore_HappyPath exercises the full direct-mode lifecycle:
// AcquireLock → OpenHandle → write to path → Commit → ReleaseLock.
// Real filesystem; no mocks.
func TestStore_HappyPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := mustBuild(t, root)

	ctx := context.Background()
	spec := store.RegionLockSpec{
		StoreName:  "content",
		Region:     []string{"reports/*"},
		ReadRegion: []string{"shared/**"},
	}

	// AcquireLock — no-op for direct mode; both results zero-valued.
	lh, cr, err := s.AcquireLock(ctx, spec)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if (lh != store.LockHandle{}) {
		t.Fatalf("AcquireLock LockHandle = %+v, want zero", lh)
	}
	if (cr != store.ClaimResult{}) {
		t.Fatalf("AcquireLock ClaimResult = %+v, want zero", cr)
	}

	// OpenHandle — supervisor would have stashed regions in ctx.
	openCtx := WithRegions(ctx, []string{"reports/*"}, []string{"shared/**"})
	nh, err := s.OpenHandle(openCtx, lh, false)
	if err != nil {
		t.Fatalf("OpenHandle: %v", err)
	}
	fh, ok := nh.(store.FilesystemDirectHandle)
	if !ok {
		t.Fatalf("OpenHandle returned %T, want FilesystemDirectHandle", nh)
	}
	if fh.Path != root {
		t.Fatalf("handle.Path = %q, want %q", fh.Path, root)
	}
	if !reflect.DeepEqual(fh.WriteRegions, []string{"reports/*"}) {
		t.Fatalf("handle.WriteRegions = %v, want %v", fh.WriteRegions, []string{"reports/*"})
	}
	if !reflect.DeepEqual(fh.ReadRegions, []string{"shared/**"}) {
		t.Fatalf("handle.ReadRegions = %v, want %v", fh.ReadRegions, []string{"shared/**"})
	}

	// Executor-equivalent write: drop a real file under the handle's path.
	dir := filepath.Join(fh.Path, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(dir, "summary.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Commit — no-op, returns Changed:true.
	commit, err := s.Commit(ctx, lh)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !commit.Changed {
		t.Fatalf("CommitResult.Changed = false, want true")
	}

	// ReleaseLock under each ReleaseAction must be a no-op.
	actions := []store.ReleaseAction{
		store.ReleaseCommit,
		store.ReleaseDiscard,
		store.ReleaseGiveUp,
		store.ReleasePreserveResume,
	}
	for _, a := range actions {
		if err := s.ReleaseLock(ctx, lh, a); err != nil {
			t.Fatalf("ReleaseLock(%q): %v", a, err)
		}
	}

	// Live region survives Commit + ReleaseLock — direct mode does not
	// scrub the live filesystem.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after release: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("file contents after release = %q, want %q", got, "hello")
	}
}

// TestStore_OpenHandle_NoRegions covers the case where the runner forgot
// to attach regions to the context — the handle still constructs but
// with empty region slices.
func TestStore_OpenHandle_NoRegions(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	nh, err := s.OpenHandle(context.Background(), store.LockHandle{}, false)
	if err != nil {
		t.Fatalf("OpenHandle: %v", err)
	}
	fh := nh.(store.FilesystemDirectHandle)
	if len(fh.WriteRegions) != 0 || len(fh.ReadRegions) != 0 {
		t.Fatalf("handle = %+v, want empty WriteRegions and ReadRegions", fh)
	}
}

// TestStore_LockEligible_AlwaysTrue confirms the unconditional return —
// the supervisor has already screened via RegionsConflict by this point.
func TestStore_LockEligible_AlwaysTrue(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	cases := []store.LockSpec{
		store.RegionLockSpec{StoreName: "x", Region: []string{"a/*"}},
		store.NamedLockSpec{Name: "n", Mode: store.LockModeMutex},
		store.ClaimLockSpec{StoreName: "x"},
	}
	for _, sp := range cases {
		ok, err := s.LockEligible(context.Background(), sp)
		if err != nil {
			t.Fatalf("LockEligible(%T): %v", sp, err)
		}
		if !ok {
			t.Fatalf("LockEligible(%T) = false, want true", sp)
		}
	}
}

// TestStore_RegionsConflict_DelegatesToHelper sanity-checks the
// any-typed wrapper: matched region types delegate to the pure helper;
// mismatched types fail-closed (return true).
func TestStore_RegionsConflict_DelegatesToHelper(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())

	// Same-typed: delegates to RegionsConflict.
	if !s.RegionsConflict([]string{"a/*"}, []string{"a/foo"}) {
		t.Fatalf("expected conflict for overlapping globs")
	}
	if s.RegionsConflict([]string{"a/*"}, []string{"b/*"}) {
		t.Fatalf("expected disjoint")
	}

	// Wrong type on either side fails closed (true).
	if !s.RegionsConflict("not-a-slice", []string{"a/*"}) {
		t.Fatalf("expected fail-closed conflict on bad type")
	}
	if !s.RegionsConflict([]string{"a/*"}, 42) {
		t.Fatalf("expected fail-closed conflict on bad type")
	}
}

// TestStore_UnmarshalRegion verifies JSONB → []string round-trip and
// rejects malformed input.
func TestStore_UnmarshalRegion(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	got, err := s.UnmarshalRegion([]byte(`["a/*", "b/foo"]`))
	if err != nil {
		t.Fatalf("UnmarshalRegion: %v", err)
	}
	globs, ok := got.([]string)
	if !ok {
		t.Fatalf("UnmarshalRegion returned %T, want []string", got)
	}
	if !reflect.DeepEqual(globs, []string{"a/*", "b/foo"}) {
		t.Fatalf("globs = %v, want %v", globs, []string{"a/*", "b/foo"})
	}

	if _, err := s.UnmarshalRegion([]byte(`{"not": "an array"}`)); err == nil {
		t.Fatalf("expected error on object input")
	}
	if _, err := s.UnmarshalRegion([]byte(`not-json`)); err == nil {
		t.Fatalf("expected error on garbage input")
	}
}

// TestStore_HasPriorWork_AlwaysFalse documents the v1 behaviour: direct
// mode never reports prior work; the executor itself is responsible for
// noticing partial state on disk.
func TestStore_HasPriorWork_AlwaysFalse(t *testing.T) {
	t.Parallel()

	s := mustBuild(t, t.TempDir())
	got, err := s.HasPriorWork(context.Background(),
		store.RegionLockSpec{StoreName: "x", Region: []string{"a/*"}})
	if err != nil {
		t.Fatalf("HasPriorWork: %v", err)
	}
	if got {
		t.Fatalf("HasPriorWork = true, want false")
	}
}

func mustBuild(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Factory{}.Build("content", map[string]any{"mode": "direct", "root": root})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s.(*Store)
}
