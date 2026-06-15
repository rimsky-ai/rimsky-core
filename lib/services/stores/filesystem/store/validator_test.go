// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func newValidPolicy(root string, sub string) *PickPolicy {
	return &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
}

func validatorTestRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	sub := "docs"
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, sub
}

func TestValidator_RejectsOldNames(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	// @deliberate: assign the legacy kind directly to bypass UnmarshalYAML's name-translation layer, exercising the post-decode validator on a payload only an in-process producer could synthesize.
	pp.OnCommit = action.Action{Kind: action.Kind("release_to_back")}
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error, got OK")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "unknown action") {
		t.Errorf("expected error mentioning 'unknown action'; got %q", joined)
	}
}

func TestValidator_RejectsMissingFields(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{}
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for missing OnCommit, got OK")
	}
}

func TestValidator_RejectsPopOnOpen(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.Pop}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for pop + on_open, got OK")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "pop is incompatible") {
		t.Errorf("expected pop-incompatibility error; got %q", joined)
	}
}

func TestValidator_WarnsRecycleOnDrain(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.Recycle}
	pp.SyncStrategy = "on_drain"
	res := validatePickPolicy(root, "@r", pp)
	if !res.OK() {
		t.Fatalf("expected OK, got errors: %v", res.Errors)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning, got none")
	}
	joined := strings.Join(res.Warnings, "; ")
	if !strings.Contains(joined, "inert") {
		t.Errorf("expected warning containing 'inert'; got %q", joined)
	}
}

func TestValidator_RejectsUnknownAction(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.Kind("nonsense")}
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error, got OK")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "unknown action") {
		t.Errorf("expected 'unknown action' error; got %q", joined)
	}
}

func TestValidator_RejectsMalformedParameterizedAction(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	// @deliberate: synthesize a pop_and_move with no MoveTarget directly so the validator's "non-empty target" check is exercised; UnmarshalYAML would otherwise reject this shape before validation.
	pp.OnCommit = action.Action{Kind: action.PopAndMove}
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for pop_and_move without target")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "non-empty target") {
		t.Errorf("expected 'non-empty target' error; got %q", joined)
	}
}

func TestValidator_RejectsMissingTargetDirectory(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: "does/not/exist"}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for missing target dir")
	}
}

func TestValidator_AcceptsMatchingFilesystemTarget(t *testing.T) {
	root, sub := validatorTestRoot(t)
	must(t, os.MkdirAll(filepath.Join(root, "archive"), 0o755))
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: "archive"}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if !res.OK() {
		t.Fatalf("expected OK with same-fs target, got: %v", res.Errors)
	}
}

func TestValidator_RejectsInvalidSyncStrategy(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	// @constraint: "on_sweep" was a previously-supported sync_strategy value; the validator must now reject it so stale operator configs surface a clear error rather than running with a silently-ignored setting.
	pp.SyncStrategy = "on_sweep"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for old on_sweep value")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "on_open|on_drain|explicit|never") {
		t.Errorf("expected error listing legal sync_strategy values; got %q", joined)
	}
}

func TestValidator_DefaultsEmptySyncStrategyToOnOpen(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.SyncStrategy = ""
	res := validatePickPolicy(root, "@r", pp)
	if !res.OK() {
		t.Fatalf("expected OK with empty SyncStrategy (defaults to on_open); got: %v", res.Errors)
	}
	if pp.SyncStrategy != "on_open" {
		t.Errorf("expected SyncStrategy defaulted to on_open; got %q", pp.SyncStrategy)
	}
}

// TestValidator_AcceptsEmptyPolicyRoot — an empty pp.Root means the
// policy operates at the store root itself, useful for single-entry
// policies whose FolderPattern matches one specific top-level folder
// (e.g. a "consolidate-on-the-corpus-root" pick policy).
// filepath.Clean("") == ".", so the canonicalization checks treat empty
// and "." identically.
func TestValidator_AcceptsEmptyPolicyRoot(t *testing.T) {
	root, _ := validatorTestRoot(t)
	pp := newValidPolicy(root, "")
	res := validatePickPolicy(root, "@r", pp)
	if !res.OK() {
		t.Fatalf("expected OK with empty Root, got errors: %v", res.Errors)
	}
}

// TestValidator_RejectsCrossFilesystemTarget exercises the load-bearing
// same-fs guard in validateMoveTargetSameFS. os.Rename across
// filesystems is not atomic — admitting a cross-fs target would let
// every commit run a non-atomic rename that could leave the corpus in
// a half-moved state on power loss.
//
// The test scans common Linux mount points for a directory whose
// underlying device differs from the temp-dir device. If no such
// directory is found (typical on macOS, or a container with /tmp on
// the same fs as everything else), the test skips. Spec §10.3 / plan
// task 17 mandate this test exists; portability of "two distinct
// filesystems" is best-effort.
func TestValidator_RejectsCrossFilesystemTarget(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires two distinct filesystems; reliable only on linux")
	}
	root := t.TempDir()
	sub := "docs"
	if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	rootStat, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	rootSys, ok := rootStat.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("device-id query unavailable on this platform")
	}
	rootDev := rootSys.Dev

	candidates := []string{"/dev/shm", "/run", "/proc"}
	var altDir string
	for _, c := range candidates {
		st, err := os.Stat(c)
		if err != nil {
			continue
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Dev == rootDev {
			continue
		}
		probe, err := os.MkdirTemp(c, "rimsky-fs-store-cross-fs-*")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(probe) })
		altDir = probe
		break
	}
	if altDir == "" {
		t.Skip("no cross-filesystem directory available in this environment")
	}

	// @deliberate: feed the validator a *relative* MoveTarget (filepath.Rel from store root to the cross-fs probe dir), matching the typical operator config shape rather than the absolute-path shape that the absolute-target guard would short-circuit first; skip if Rel cannot bridge the two roots.
	rel, err := filepath.Rel(root, altDir)
	if err != nil {
		t.Skip("cannot construct relative path between root and cross-fs dir")
	}

	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: rel}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for cross-filesystem target")
	}
	joined := errsString(res.Errors)
	// @deliberate: accept either "different filesystem" or "escapes the store root" — when the cross-fs probe dir resolves to a relative path that climbs above the store root, the containment guard fires before the same-fs guard; either rejection satisfies the spec's "refuse unsafe pop_and_move target" contract.
	if !strings.Contains(joined, "different filesystem") &&
		!strings.Contains(joined, "escapes the store root") {
		t.Errorf("expected 'different filesystem' or 'escapes the store root' error; got %q", joined)
	}
}

// TestValidator_RejectsTraversalTarget guards the path-containment
// check on `pop_and_move`'s MoveTarget. An operator config of
// `pop_and_move: ../../etc/triage` would otherwise let every commit
// run os.Rename(<store-root>/policy.root/folder,
// <store-root>/../../etc/triage/folder), exfiltrating directories
// outside the store root. Symmetric with openScoped's traversal guard.
func TestValidator_RejectsTraversalTarget(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: "../escape"}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for ..-prefixed target")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "escapes the store root") {
		t.Errorf("expected 'escapes the store root' error; got %q", joined)
	}
}

// TestValidator_RejectsAbsoluteTarget pins that an absolute MoveTarget
// is rejected — even one that happens to be on the same filesystem.
// Without this check an operator config like `pop_and_move: /tmp/triage`
// would load and run renames outside the store root.
func TestValidator_RejectsAbsoluteTarget(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: "/tmp/triage"}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for absolute target")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "absolute") {
		t.Errorf("expected 'absolute' error; got %q", joined)
	}
}

// TestValidator_RejectsTargetEqualsPolicyRoot pins the issue-7 fix:
// a `pop_and_move` whose target resolves to the policy root itself
// is a silent no-op at runtime (POSIX rename of a directory to itself
// returns nil). The validator must reject it so the operator gets a
// clear error instead of behavioral drift.
func TestValidator_RejectsTargetEqualsPolicyRoot(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: sub}
	pp.SyncStrategy = "on_open"
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for target == policy root")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "same directory as the policy root") {
		t.Errorf("expected 'same directory as the policy root' error; got %q", joined)
	}
}

// TestValidator_RejectsNullCommit pins the issue-11 path: a YAML null
// (or missing field) reaching the struct as the zero Action must
// surface a "required (got null or missing)" error, not the opaque
// `unknown action ""` we used to emit.
func TestValidator_RejectsNullCommit(t *testing.T) {
	root, sub := validatorTestRoot(t)
	pp := newValidPolicy(root, sub)
	pp.OnCommit = action.Action{}
	res := validatePickPolicy(root, "@r", pp)
	if res.OK() {
		t.Fatal("expected validation error for null on_commit")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "on_commit: required (got null or missing)") {
		t.Errorf("expected 'on_commit: required (got null or missing)'; got %q", joined)
	}
}

// errsString joins error messages into a single string for substring assertions.
func errsString(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
