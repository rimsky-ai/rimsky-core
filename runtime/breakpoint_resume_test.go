// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoint_resume_test.go — exercises ValidateAndPersistResume against
// a SQLite-backed in-memory persistence. Covers the no-overlay paths,
// idempotent replay, and overlay-with-schema validation by seeding
// `snapshot.effective_schema` directly. `buildSnapshot` populates that
// field in production via `CheckpointContext.EffectiveSchema`; tests
// here pre-seed the same key so the seeded-schema validation path
// matches the production wire shape.
//
// @concept: breakpoint

package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	_ "github.com/fallguyconsulting/rimsky/foundation/persistence/sqlite"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/runtime"
)

// seedBreakpointResumeFixture creates a template + main run scope +
// instance + a breakpoint and returns (instanceID, breakpointID). Used
// as the FK target for hit rows.
func seedBreakpointResumeFixture(t *testing.T, ctx context.Context, tables persistence.Tables) (shared.UUID, shared.UUID) {
	t.Helper()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:                "breakpoint-resume-fixture",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

	var bpID shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		id, err := tables.Breakpoints().Create(ctx, persistence.BreakpointRow{
			InstanceID:     instanceID,
			Matcher:        map[string]any{},
			Checkpoint:     persistence.CheckpointBeforeDispatch,
			Mode:           persistence.BreakpointModePause,
			OverflowPolicy: persistence.OverflowBlockDispatch,
			HitTTLSeconds:  300,
			CreatedByKey:   "test-key",
		}, tx)
		if err != nil {
			return err
		}
		bpID = id
		return nil
	}); err != nil {
		t.Fatalf("seedBreakpointResumeFixture: %v", err)
	}
	return instanceID, bpID
}

// createHitWithSnapshot inserts a before_dispatch hit and returns its id.
func createHitWithSnapshot(t *testing.T, ctx context.Context, tables persistence.Tables, bpID, instanceID shared.UUID, snapshot map[string]any) shared.UUID {
	t.Helper()
	return createHitOfCheckpoint(t, ctx, tables, bpID, instanceID, snapshot, persistence.CheckpointBeforeDispatch)
}

// createHitOfCheckpoint inserts a hit of the named checkpoint kind and
// returns its id. Used by tests that need to exercise the
// after_terminal-specific resume gates.
func createHitOfCheckpoint(t *testing.T, ctx context.Context, tables persistence.Tables, bpID, instanceID shared.UUID, snapshot map[string]any, checkpoint persistence.BreakpointCheckpoint) shared.UUID {
	t.Helper()
	var id shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		hid, _, err := tables.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID,
			InstanceID:   instanceID,
			Checkpoint:   checkpoint,
			Mode:         persistence.BreakpointModePause,
			Snapshot:     snapshot,
		}, tx)
		if err != nil {
			return err
		}
		id = hid
		return nil
	}); err != nil {
		t.Fatalf("create hit: %v", err)
	}
	return id
}

func openInMemoryTables(t *testing.T) persistence.Tables {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Tables()
}

// TestValidateAndPersistResume_NotFound covers the 404 path.
func TestValidateAndPersistResume_NotFound(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}

	_, err := runtime.ValidateAndPersistResume(ctx, args, shared.UUID(uuid.New()), nil, "operator")
	if !errors.Is(err, shared.ErrBreakpointHitNotFound) {
		t.Fatalf("expected ErrBreakpointHitNotFound, got %v", err)
	}
}

// TestValidateAndPersistResume_FirstResumeNoOverlay covers the happy
// path with no overlay (no schema validation needed).
func TestValidateAndPersistResume_FirstResumeNoOverlay(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	hitID := createHitWithSnapshot(t, ctx, tables, bpID, instanceID, map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{"k": "v"},
		},
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	res, err := runtime.ValidateAndPersistResume(ctx, args, hitID, nil, "operator")
	if err != nil {
		t.Fatalf("ValidateAndPersistResume: %v", err)
	}
	if !res.FirstResume {
		t.Errorf("FirstResume: got false want true")
	}

	// Confirm the persistence row reflects the resume.
	var got *persistence.BreakpointHitRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		got, err = tables.BreakpointHits().Get(ctx, hitID, tx)
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResumedAt == nil {
		t.Errorf("ResumedAt nil after resume")
	}
	if got.ResumedByKey == nil || *got.ResumedByKey != "operator" {
		t.Errorf("ResumedByKey: got %v want operator", got.ResumedByKey)
	}
}

// TestValidateAndPersistResume_IdempotentReplay confirms a second
// resume on the same hit returns FirstResume=false and does not error.
func TestValidateAndPersistResume_IdempotentReplay(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	hitID := createHitWithSnapshot(t, ctx, tables, bpID, instanceID, map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{},
		},
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	if _, err := runtime.ValidateAndPersistResume(ctx, args, hitID, nil, "operator"); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	res, err := runtime.ValidateAndPersistResume(ctx, args, hitID, nil, "operator")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.FirstResume {
		t.Errorf("FirstResume on replay: got true want false")
	}
}

// TestValidateAndPersistResume_OverlayNoSchema covers the
// snapshot-lacks-effective-schema fallback: validation is skipped (with
// a Warn log), the overlay still persists. In production
// `buildSnapshot` always populates `effective_schema` on
// before_dispatch hits; this test forces the absent-schema branch by
// omitting the field from the seeded snapshot.
func TestValidateAndPersistResume_OverlayNoSchema(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	hitID := createHitWithSnapshot(t, ctx, tables, bpID, instanceID, map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{"base": "x"},
		},
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	overlay := map[string]any{"override": 1}
	if _, err := runtime.ValidateAndPersistResume(ctx, args, hitID, overlay, "operator"); err != nil {
		t.Fatalf("ValidateAndPersistResume: %v", err)
	}

	var got *persistence.BreakpointHitRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		got, err = tables.BreakpointHits().Get(ctx, hitID, tx)
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResumeOverlay == nil {
		t.Errorf("ResumeOverlay nil after resume with overlay")
	}
}

// TestValidateAndPersistResume_OverlaySchemaRejects validates the
// "schema present" branch by seeding `effective_schema` into the
// snapshot and using an overlay that violates a `type` constraint. The
// shipped `buildSnapshot` produces the same shape in production via
// `CheckpointContext.EffectiveSchema`.
func TestValidateAndPersistResume_OverlaySchemaRejects(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	// Schema: top-level requires `score: number`.
	snapshot := map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{"score": float64(10)},
		},
		"effective_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"score": map[string]any{"type": "number"},
			},
			"additionalProperties": false,
		},
	}
	hitID := createHitWithSnapshot(t, ctx, tables, bpID, instanceID, snapshot)

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	// Overlay sets `score` to a string — rejected by `type: number`.
	overlay := map[string]any{"score": "not-a-number"}
	_, err := runtime.ValidateAndPersistResume(ctx, args, hitID, overlay, "operator")
	if !errors.Is(err, shared.ErrResumeOverlayInvalid) {
		t.Fatalf("expected ErrResumeOverlayInvalid, got %v", err)
	}
}

// TestValidateAndPersistResume_AfterTerminalOverlayRejected pins the
// rule that overlays cannot be applied to after_terminal hits: the
// dispatch is already committed, so an overlay could never feed back
// into the run. The previous behavior accepted the overlay and persisted
// it into `resume_overlay`, but the caller discards the merged bag at
// the after_terminal call sites — silently no-op. Now the resume API
// rejects with ErrResumeOverlayInvalid + leaves the hit unresumed so
// the operator gets a clear diagnostic.
func TestValidateAndPersistResume_AfterTerminalOverlayRejected(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	hitID := createHitOfCheckpoint(t, ctx, tables, bpID, instanceID, map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{"base": "x"},
		},
	}, persistence.CheckpointAfterTerminal)

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	overlay := map[string]any{"override": 1}
	_, err := runtime.ValidateAndPersistResume(ctx, args, hitID, overlay, "operator")
	if !errors.Is(err, shared.ErrResumeOverlayInvalid) {
		t.Fatalf("expected ErrResumeOverlayInvalid on after_terminal overlay, got %v", err)
	}

	// The hit must remain unresumed — the rejection happens before the
	// Resume call, so the row's state didn't move.
	var got *persistence.BreakpointHitRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		got, err = tables.BreakpointHits().Get(ctx, hitID, tx)
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResumedAt != nil {
		t.Errorf("hit must remain unresumed after rejection; got ResumedAt=%v", got.ResumedAt)
	}
	if got.ResumeOverlay != nil {
		t.Errorf("hit must NOT carry the rejected overlay; got ResumeOverlay=%v", got.ResumeOverlay)
	}

	// Resuming the same hit with NO overlay should succeed — the
	// after_terminal hit accepts the no-overlay case (it's just the
	// notification half of the protocol).
	res, err := runtime.ValidateAndPersistResume(ctx, args, hitID, nil, "operator")
	if err != nil {
		t.Fatalf("ValidateAndPersistResume(no overlay): %v", err)
	}
	if !res.FirstResume {
		t.Errorf("FirstResume on after_terminal no-overlay resume: got false want true")
	}
}
