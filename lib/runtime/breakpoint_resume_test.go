// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func seedBreakpointResumeFixture(t *testing.T, ctx context.Context, tables persistence.Tables) (shared.UUID, shared.UUID) {
	t.Helper()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "breakpoint-resume-fixture",
		Version: "1",
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
			ID:           instanceID,
			TemplateHash: templateHash,
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

func createHitWithSnapshot(t *testing.T, ctx context.Context, tables persistence.Tables, bpID, instanceID shared.UUID, snapshot map[string]any) shared.UUID {
	t.Helper()
	return createHitOfCheckpoint(t, ctx, tables, bpID, instanceID, snapshot, persistence.CheckpointBeforeDispatch)
}

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

func TestValidateAndPersistResume_NotFound(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}

	_, err := runtime.ValidateAndPersistResume(ctx, args, shared.UUID(uuid.New()), nil, "operator")
	if !errors.Is(err, shared.ErrBreakpointHitNotFound) {
		t.Fatalf("expected ErrBreakpointHitNotFound, got %v", err)
	}
}

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

func TestValidateAndPersistResume_ConcurrentFirstResumeExactlyOneWins(t *testing.T) {
	const racers = 8

	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
	hitID := createHitWithSnapshot(t, ctx, tables, bpID, instanceID, map[string]any{
		"dispatch_context": map[string]any{
			"merged_attributes": map[string]any{},
		},
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}

	start := make(chan struct{})
	var ready, done sync.WaitGroup
	firstResume := make([]bool, racers)
	errs := make(chan error, racers)
	ready.Add(racers)
	done.Add(racers)
	for i := 0; i < racers; i++ {
		go func(idx int) {
			defer done.Done()
			ready.Done()
			<-start
			overlay := map[string]any{"racer": idx}
			res, err := runtime.ValidateAndPersistResume(ctx, args, hitID, overlay, "racer")
			if err != nil {
				errs <- err
				return
			}
			firstResume[idx] = res.FirstResume
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent resume: %v", err)
	}

	winners := 0
	for _, r := range firstResume {
		if r {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent first-resume: got %d winners want exactly 1 (firstResume=%v)", winners, firstResume)
	}
}

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

func TestValidateAndPersistResume_OverlaySchemaRejects(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID, bpID := seedBreakpointResumeFixture(t, ctx, tables)
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
	overlay := map[string]any{"score": "not-a-number"}
	_, err := runtime.ValidateAndPersistResume(ctx, args, hitID, overlay, "operator")
	if !errors.Is(err, shared.ErrResumeOverlayInvalid) {
		t.Fatalf("expected ErrResumeOverlayInvalid, got %v", err)
	}
}

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

	res, err := runtime.ValidateAndPersistResume(ctx, args, hitID, nil, "operator")
	if err != nil {
		t.Fatalf("ValidateAndPersistResume(no overlay): %v", err)
	}
	if !res.FirstResume {
		t.Errorf("FirstResume on after_terminal no-overlay resume: got false want true")
	}
}
