// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoint_eval_test.go — exercises runtime.EvaluateBreakpoints
// against a SQLite-backed in-memory persistence. Covers matcher
// mismatch, signal_type filtering on after_terminal, pause-mode
// blocking + overlay merge, notify_only non-blocking, queue-cap
// overflow under drop_oldest + block_dispatch, and the
// cascade-deleted-during-wait race.
//
// Reuses openInMemoryTables from breakpoint_resume_test.go (same
// package); a local seedBreakpointEvalFixture replaces the
// resume-test fixture because the latter pre-seeds a pause-mode
// breakpoint that would interfere with these tests' control over
// which breakpoints are present on the instance.
//
// @concept: breakpoint

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// seedBreakpointEvalFixture is a stripped-down variant of
// seedBreakpointResumeFixture (in breakpoint_resume_test.go) that
// creates a template + main run scope + instance but does NOT
// pre-seed a breakpoint — the eval-tests want full control over
// which breakpoints exist on the instance.
func seedBreakpointEvalFixture(t *testing.T, ctx context.Context, tables persistence.Tables) shared.UUID {
	t.Helper()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:                "breakpoint-eval-fixture-" + uuid.NewString(),
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

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
		return nil
	}); err != nil {
		t.Fatalf("seedBreakpointEvalFixture: %v", err)
	}
	return instanceID
}

// newCheckpointContext returns a CheckpointContext seeded with the
// fixture's instance id and synthetic UUIDs for DispatchID / FrameID.
// Tests override the per-case fields directly.
func newCheckpointContext(instanceID shared.UUID) runtime.CheckpointContext {
	return runtime.CheckpointContext{
		InstanceID:       instanceID,
		DispatchID:       shared.UUID(uuid.New()),
		FrameID:          shared.UUID(uuid.New()),
		Executor:         "test-executor",
		NodeType:         "fixture-node-type",
		Graph:            "main",
		ChildKey:         "",
		MergedAttributes: map[string]any{"k": "v"},
		Checkpoint:       persistence.CheckpointBeforeDispatch,
	}
}

// createBreakpointForEval inserts a breakpoint and returns its id.
func createBreakpointForEval(t *testing.T, ctx context.Context, tables persistence.Tables, bp persistence.BreakpointRow) shared.UUID {
	t.Helper()
	var id shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		id, err = tables.Breakpoints().Create(ctx, bp, tx)
		return err
	}); err != nil {
		t.Fatalf("create breakpoint: %v", err)
	}
	return id
}

// listHitsForBreakpoint returns every hit row currently on a breakpoint.
func listHitsForBreakpoint(t *testing.T, ctx context.Context, tables persistence.Tables, bpID shared.UUID) []persistence.BreakpointHitRow {
	t.Helper()
	var out []persistence.BreakpointHitRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := tables.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 1000, tx)
		out = rows
		return err
	}); err != nil {
		t.Fatalf("list hits: %v", err)
	}
	return out
}

// resumeHit calls BreakpointHits.Resume directly, bypassing the
// validation discipline in ValidateAndPersistResume — these tests
// drive the evaluator, not the resume path.
func resumeHit(t *testing.T, ctx context.Context, tables persistence.Tables, hitID shared.UUID, overlay map[string]any) {
	t.Helper()
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.BreakpointHits().Resume(ctx, hitID, "test-resumer", overlay, tx)
	}); err != nil {
		t.Fatalf("resume hit: %v", err)
	}
}

// TestEvaluateBreakpoints_MatcherMismatchNoHit confirms that a
// matcher that doesn't fire produces no hit row.
func TestEvaluateBreakpoints_MatcherMismatchNoHit(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{"node_type": "no-such-type"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}
	hits := listHitsForBreakpoint(t, ctx, tables, bpID)
	if len(hits) != 0 {
		t.Fatalf("expected zero hits on matcher mismatch; got %d", len(hits))
	}
}

// TestEvaluateBreakpoints_SignalTypePrefixMatchAndMiss covers the
// after_terminal-only signal_type prefix filter (spec §4.5). Two
// breakpoints in the same checkpoint: one with prefix matching the
// terminal signal (fires), one with a prefix that doesn't (skipped).
func TestEvaluateBreakpoints_SignalTypePrefixMatchAndMiss(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	// Trailing-`*` is the project's wildcard syntax per signal/types.go;
	// without it HasPrefix does exact-equality only.
	matchPrefix := "terminal/error/*"
	missPrefix := "terminal/success"
	bpMatch := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointAfterTerminal,
		SignalType:     &matchPrefix,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	bpMiss := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointAfterTerminal,
		SignalType:     &missPrefix,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	cc.Checkpoint = persistence.CheckpointAfterTerminal
	sig := signalpkg.Signal{Type: signalpkg.TypePath("terminal/error/timeout")}
	cc.TerminalSignal = &sig

	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}
	if got := len(listHitsForBreakpoint(t, ctx, tables, bpMatch)); got != 1 {
		t.Fatalf("matching signal_type breakpoint: expected 1 hit, got %d", got)
	}
	if got := len(listHitsForBreakpoint(t, ctx, tables, bpMiss)); got != 0 {
		t.Fatalf("missing signal_type breakpoint: expected 0 hits, got %d", got)
	}
}

// TestEvaluateBreakpoints_NotifyOnlyDoesNotBlock confirms notify_only
// breakpoints write a hit row and return immediately.
func TestEvaluateBreakpoints_NotifyOnlyDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
			t.Errorf("EvaluateBreakpoints: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("notify_only EvaluateBreakpoints did not return within 2s")
	}
	if got := len(listHitsForBreakpoint(t, ctx, tables, bpID)); got != 1 {
		t.Fatalf("expected 1 hit row, got %d", got)
	}
}

// TestEvaluateBreakpoints_PauseModeBlocksAndAppliesOverlay confirms
// pause-mode breakpoints block until the hit is resumed, and that any
// resume_overlay is deep-merged into the returned MergedAttributes
// bag.
func TestEvaluateBreakpoints_PauseModeBlocksAndAppliesOverlay(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	cc.MergedAttributes = map[string]any{"base": "x"}

	type evalResult struct {
		merged map[string]any
		err    error
	}
	resultCh := make(chan evalResult, 1)
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	// Wait for the hit row to appear (proves the goroutine is blocked
	// inside waitForResume).
	waitForHit := func() shared.UUID {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			hits := listHitsForBreakpoint(t, ctx, tables, bpID)
			if len(hits) == 1 {
				return hits[0].ID
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("hit row never appeared")
		return shared.UUID{}
	}
	hitID := waitForHit()

	// Confirm the goroutine is still blocked.
	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned before resume")
	case <-time.After(100 * time.Millisecond):
	}

	// Resume with an overlay that adds a key and overrides `base`.
	resumeHit(t, ctx, tables, hitID, map[string]any{"base": "y", "added": 1})

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints error: %v", res.err)
		}
		if got := res.merged["base"]; got != "y" {
			t.Errorf("base after merge: got %v want y", got)
		}
		// Overlay value 1 (Go int at write site) round-trips through
		// JSON as float64; compare with that in mind.
		got := res.merged["added"]
		if gotF, ok := got.(float64); !ok || gotF != 1 {
			t.Errorf("added after merge: got %v (%T) want 1 (numeric)", got, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after resume within 2s")
	}
}

// TestEvaluateBreakpoints_CascadeDeletedHitTreatedAsAutoResume covers
// the race where the hit row is cascade-deleted (via parent breakpoint
// delete) while waitForResume is polling. The poll returns nil hit and
// EvaluateBreakpoints continues with the original attribute bag.
func TestEvaluateBreakpoints_CascadeDeletedHitTreatedAsAutoResume(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	cc.MergedAttributes = map[string]any{"base": "x"}

	type evalResult struct {
		merged map[string]any
		err    error
	}
	resultCh := make(chan evalResult, 1)
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	// Wait for the hit row, then delete the parent breakpoint to
	// trigger ON DELETE CASCADE.
	deadline := time.Now().Add(2 * time.Second)
	for {
		hits := listHitsForBreakpoint(t, ctx, tables, bpID)
		if len(hits) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hit row never appeared")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.Breakpoints().Delete(ctx, bpID, tx)
	}); err != nil {
		t.Fatalf("delete breakpoint: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints error: %v", res.err)
		}
		if got := res.merged["base"]; got != "x" {
			t.Errorf("base after cascade-delete-while-waiting: got %v want x", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after cascade delete within 2s")
	}
}

// TestEvaluateBreakpoints_OverflowDropOldestEvictsAndIncrementsCounter
// confirms the drop_oldest overflow policy: once the per-breakpoint
// unresumed-hit queue reaches the cap, the oldest unresumed hit is
// deleted and the breakpoint's dropped_count increments.
//
// To keep the test fast, the in-test cap is the production constant
// (breakpointQueueCap = 100); we pre-seed 100 unresumed rows directly
// via the persistence layer (bypassing EvaluateBreakpoints), then
// invoke EvaluateBreakpoints once and observe the eviction.
func TestEvaluateBreakpoints_OverflowDropOldestEvictsAndIncrementsCounter(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	// Seed 100 unresumed hits directly (queue cap = 100).
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 100; i++ {
			if _, _, err := tables.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   instanceID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModeNotifyOnly,
				Snapshot:     map[string]any{"seed": i},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed 100 hits: %v", err)
	}

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}

	// Cap stays at 100 (one evicted, one written).
	hits := listHitsForBreakpoint(t, ctx, tables, bpID)
	if len(hits) != 100 {
		t.Errorf("expected 100 hit rows after drop_oldest + write, got %d", len(hits))
	}
	// dropped_count incremented.
	var bp *persistence.BreakpointRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		bp, err = tables.Breakpoints().Get(ctx, bpID, tx)
		return err
	}); err != nil {
		t.Fatalf("get breakpoint: %v", err)
	}
	if bp.DroppedCount < 1 {
		t.Errorf("expected dropped_count >= 1, got %d", bp.DroppedCount)
	}
}

// TestEvaluateBreakpoints_OverflowBlockDispatchReturnsWhenDrained
// confirms that under block_dispatch the evaluator blocks the
// hit-write while the queue is full, then proceeds once a hit is
// resumed (draining the queue).
func TestEvaluateBreakpoints_OverflowBlockDispatchReturnsWhenDrained(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	// Seed 100 unresumed hits to hit the cap.
	hitIDs := make([]shared.UUID, 0, 100)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 100; i++ {
			id, _, err := tables.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   instanceID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModePause,
				Snapshot:     map[string]any{"seed": i},
			}, tx)
			if err != nil {
				return err
			}
			hitIDs = append(hitIDs, id)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed 100 hits: %v", err)
	}

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	type evalResult struct {
		merged map[string]any
		err    error
	}
	resultCh := make(chan evalResult, 1)
	var startOnce sync.Once
	go func() {
		startOnce.Do(func() {})
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	// Confirm the evaluator is blocked in handleOverflow (no new hit
	// row written while the queue is at cap).
	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned while queue at cap")
	case <-time.After(400 * time.Millisecond):
	}

	// Resume one of the seeded hits to drop the unresumed count below
	// the cap.
	resumeHit(t, ctx, tables, hitIDs[0], nil)

	// Now the evaluator should write its own hit row. Since this is a
	// pause-mode breakpoint, after writing it will block in
	// waitForResume — find the new hit and resume it.
	deadline := time.Now().Add(2 * time.Second)
	var newHitID shared.UUID
	for time.Now().Before(deadline) {
		hits := listHitsForBreakpoint(t, ctx, tables, bpID)
		// 100 original seeded - 1 resumed + 1 new = 100; but we need
		// the unresumed-and-new row. Find the one whose snapshot lacks
		// our seed marker.
		for _, h := range hits {
			if h.ResumedAt != nil {
				continue
			}
			if _, isSeed := h.Snapshot["seed"]; !isSeed {
				newHitID = h.ID
				break
			}
		}
		if newHitID != (shared.UUID{}) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if newHitID == (shared.UUID{}) {
		t.Fatalf("evaluator never wrote its own hit row after drain")
	}
	resumeHit(t, ctx, tables, newHitID, nil)

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints err: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after final resume")
	}
}

// TestBuildSnapshot_IncludesEffectiveSchema confirms that the snapshot
// payload buildSnapshot writes carries the effective_schema field so
// runtime/breakpoint_resume.go::lookupEffectiveSchemaForHit engages on
// resume-time overlay validation (the load-bearing Pass 5 plumbing
// noted in runtime/breakpoint_resume.go's package comment).
func TestBuildSnapshot_IncludesEffectiveSchema(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	cc.EffectiveSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"k": map[string]any{"type": "string"},
		},
	}

	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}
	hits := listHitsForBreakpoint(t, ctx, tables, bpID)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	schema, ok := hits[0].Snapshot["effective_schema"].(map[string]any)
	if !ok || len(schema) == 0 {
		t.Fatalf("snapshot.effective_schema missing or empty: %#v", hits[0].Snapshot["effective_schema"])
	}
	if got, _ := schema["type"].(string); got != "object" {
		t.Errorf("effective_schema.type: got %v want object", got)
	}
}

// TestEvaluateBreakpoints_CtxCancelDuringOverflowBlock pins the cycle 2
// wrapping at runtime/breakpoint_eval.go::handleOverflow. The
// block_dispatch overflow loop must surface ctx-cancellation as
// *BreakpointInfraError{Phase: "ctx_cancelled"} so the
// runner_dispatch.go errors.As(*BreakpointInfraError) type-switch routes
// it through the debugger-infra path. Returning a bare ctx.Err() would
// skip the type-switch and surface as `template_resolution_failed` to
// operators (the wrong diagnostic class for a supervisor shutdown).
func TestEvaluateBreakpoints_CtxCancelDuringOverflowBlock(t *testing.T) {
	parent := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, parent, tables)
	bpID := createBreakpointForEval(t, parent, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	// Seed 100 unresumed hits so the queue is at cap; handleOverflow
	// will spin in the block-dispatch loop until ctx cancels.
	if err := tables.Transaction(parent, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 100; i++ {
			if _, _, err := tables.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   instanceID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModePause,
				Snapshot:     map[string]any{"seed": i},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed 100 hits: %v", err)
	}

	ctx, cancel := context.WithCancel(parent)
	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	type evalResult struct {
		merged map[string]any
		err    error
	}
	resultCh := make(chan evalResult, 1)
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	// Give the goroutine time to enter the overflow-block spin (one
	// poll-interval is enough; we add slack to avoid flake under load).
	time.Sleep(400 * time.Millisecond)
	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned before cancel (queue at cap should block)")
	default:
	}

	cancel()

	select {
	case res := <-resultCh:
		var infraErr *runtime.BreakpointInfraError
		if !errors.As(res.err, &infraErr) {
			t.Fatalf("expected *BreakpointInfraError on ctx cancel, got %T: %v", res.err, res.err)
		}
		if infraErr.Phase != "ctx_cancelled" {
			t.Errorf("Phase: got %q want %q", infraErr.Phase, "ctx_cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return within 2s of cancel")
	}
}

// TestEvaluateBreakpoints_CtxCancelDuringWaitForResume pins the cycle 2
// wrapping at runtime/breakpoint_eval.go::waitForResume. A ctx-cancel
// while the pause-mode hit is unresumed must surface as
// *BreakpointInfraError{Phase: "ctx_cancelled"}, for the same
// type-switch reason as the overflow case above.
func TestEvaluateBreakpoints_CtxCancelDuringWaitForResume(t *testing.T) {
	parent := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, parent, tables)
	bpID := createBreakpointForEval(t, parent, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	ctx, cancel := context.WithCancel(parent)
	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	type evalResult struct {
		merged map[string]any
		err    error
	}
	resultCh := make(chan evalResult, 1)
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	// Wait for the hit row to appear — proves the goroutine has
	// reached waitForResume and is polling.
	deadline := time.Now().Add(2 * time.Second)
	for {
		hits := listHitsForBreakpoint(t, parent, tables, bpID)
		if len(hits) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hit row never appeared (goroutine never reached waitForResume)")
		}
		time.Sleep(25 * time.Millisecond)
	}

	cancel()

	select {
	case res := <-resultCh:
		var infraErr *runtime.BreakpointInfraError
		if !errors.As(res.err, &infraErr) {
			t.Fatalf("expected *BreakpointInfraError on ctx cancel, got %T: %v", res.err, res.err)
		}
		if infraErr.Phase != "ctx_cancelled" {
			t.Errorf("Phase: got %q want %q", infraErr.Phase, "ctx_cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return within 2s of cancel")
	}
}

// TestEvaluateBreakpoints_DropOldestPhaseLabel pins the cycle 2
// rename: a tx-failure during the drop_oldest path must surface as
// *BreakpointInfraError{Phase: "drop_oldest"}, distinct from the
// "overflow_check" label used by the UnresumedCount check above it.
// The two labels disambiguate which step of the overflow handler
// failed in error logs.
func TestEvaluateBreakpoints_DropOldestPhaseLabel(t *testing.T) {
	ctx := context.Background()
	inner := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, inner)
	bpID := createBreakpointForEval(t, ctx, inner, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	// Seed 100 unresumed hits so handleOverflow takes the drop_oldest
	// branch on the next call.
	if err := inner.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 100; i++ {
			if _, _, err := inner.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   instanceID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModeNotifyOnly,
				Snapshot:     map[string]any{"seed": i},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed 100 hits: %v", err)
	}

	wrapped := &failingHitsTables{Tables: inner, hits: &failingHits{
		BreakpointHitTable: inner.BreakpointHits(),
		failDropOldest:     errors.New("injected drop_oldest failure"),
	}}
	args := runtime.RunArgs{Persist: wrapped, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	_, err := runtime.EvaluateBreakpoints(ctx, args, cc)
	var infraErr *runtime.BreakpointInfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("expected *BreakpointInfraError, got %T: %v", err, err)
	}
	if infraErr.Phase != "drop_oldest" {
		t.Errorf("Phase: got %q want %q", infraErr.Phase, "drop_oldest")
	}
}

// TestEvaluateBreakpoints_OverflowCheckPhaseLabel pins the second of
// the two distinct labels: a tx-failure during the UnresumedCount
// pre-check inside handleOverflow must surface as
// *BreakpointInfraError{Phase: "overflow_check"}.
func TestEvaluateBreakpoints_OverflowCheckPhaseLabel(t *testing.T) {
	ctx := context.Background()
	inner := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, inner)
	createBreakpointForEval(t, ctx, inner, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	wrapped := &failingHitsTables{Tables: inner, hits: &failingHits{
		BreakpointHitTable: inner.BreakpointHits(),
		failUnresumedCount: errors.New("injected UnresumedCount failure"),
	}}
	args := runtime.RunArgs{Persist: wrapped, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	_, err := runtime.EvaluateBreakpoints(ctx, args, cc)
	var infraErr *runtime.BreakpointInfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("expected *BreakpointInfraError, got %T: %v", err, err)
	}
	if infraErr.Phase != "overflow_check" {
		t.Errorf("Phase: got %q want %q", infraErr.Phase, "overflow_check")
	}
}

// failingHitsTables is a minimal persistence.Tables decorator that
// substitutes the BreakpointHits() accessor with a failure-injecting
// shim. Used by the Phase-label tests above to exercise the two
// distinct labels inside handleOverflow.
type failingHitsTables struct {
	persistence.Tables
	hits *failingHits
}

func (f *failingHitsTables) BreakpointHits() persistence.BreakpointHitTable {
	return f.hits
}

// failingHits wraps a persistence.BreakpointHitTable and optionally
// injects errors into DropOldest / UnresumedCount, which are the two
// hit-table calls handleOverflow makes (UnresumedCount on entry to the
// loop, DropOldest under the drop_oldest branch). All other methods
// delegate to the embedded inner table.
type failingHits struct {
	persistence.BreakpointHitTable
	failDropOldest     error
	failUnresumedCount error
}

func (f *failingHits) DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx persistence.Tx) (int, error) {
	if f.failDropOldest != nil {
		return 0, fmt.Errorf("drop_oldest: %w", f.failDropOldest)
	}
	return f.BreakpointHitTable.DropOldest(ctx, bpID, keepCount, tx)
}

func (f *failingHits) UnresumedCount(ctx context.Context, bpID shared.UUID, tx persistence.Tx) (int, error) {
	if f.failUnresumedCount != nil {
		return 0, fmt.Errorf("unresumed_count: %w", f.failUnresumedCount)
	}
	return f.BreakpointHitTable.UnresumedCount(ctx, bpID, tx)
}
