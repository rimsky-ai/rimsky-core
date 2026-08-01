// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func seedBreakpointEvalFixture(t *testing.T, ctx context.Context, tables persistence.Tables) shared.UUID {
	t.Helper()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "breakpoint-eval-fixture-" + uuid.NewString(),
		Version: "1",
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
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedBreakpointEvalFixture: %v", err)
	}
	return instanceID
}

func newCheckpointContext(instanceID shared.UUID) runtime.CheckpointContext {
	return runtime.CheckpointContext{
		InstanceID:       instanceID,
		NodeRunID:        shared.UUID(uuid.New()),
		FrameID:          shared.UUID(uuid.New()),
		Executor:         "test-executor",
		NodeType:         "fixture-node-type",
		Graph:            "main",
		ChildKey:         "",
		MergedAttributes: map[string]any{"k": "v"},
		Checkpoint:       persistence.CheckpointBeforeDispatch,
	}
}

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

func resumeHit(t *testing.T, ctx context.Context, tables persistence.Tables, hitID shared.UUID, overlay map[string]any) {
	t.Helper()
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := tables.BreakpointHits().Resume(ctx, hitID, "test-resumer", overlay, tx)
		return err
	}); err != nil {
		t.Fatalf("resume hit: %v", err)
	}
}

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

func TestEvaluateBreakpoints_SignalTypePrefixMatchAndMiss(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
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

func TestEvaluateBreakpoints_SignalTypeFilterFailsClosedOnNilSignal(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	anyPrefix := "terminal/*"
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointAfterTerminal,
		SignalType:     &anyPrefix,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)
	cc.Checkpoint = persistence.CheckpointAfterTerminal
	cc.TerminalSignal = nil

	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}
	if got := len(listHitsForBreakpoint(t, ctx, tables, bpID)); got != 0 {
		t.Fatalf("signal_type-filtered breakpoint with nil TerminalSignal must fail closed (no hit); got %d hits", got)
	}
}

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

	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned before resume")
	case <-time.After(100 * time.Millisecond):
	}

	resumeHit(t, ctx, tables, hitID, map[string]any{"base": "y", "added": 1})

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints error: %v", res.err)
		}
		if got := res.merged["base"]; got != "y" {
			t.Errorf("base after merge: got %v want y", got)
		}
		got := res.merged["added"]
		if gotF, ok := got.(float64); !ok || gotF != 1 {
			t.Errorf("added after merge: got %v (%T) want 1 (numeric)", got, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after resume within 2s")
	}
}

func TestEvaluateBreakpoints_LaterBreakpointSeesEarlierResumeOverlay(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bp1ID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowBlockDispatch,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
	bp2ID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID: instanceID,
		Matcher: map[string]any{
			"attrs": map[string]any{"added": float64(1)},
		},
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

	waitForHit := func(bpID shared.UUID) shared.UUID {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			hits := listHitsForBreakpoint(t, ctx, tables, bpID)
			if len(hits) == 1 {
				return hits[0].ID
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("hit row for breakpoint %s never appeared", bpID)
		return shared.UUID{}
	}

	hit1ID := waitForHit(bp1ID)
	resumeHit(t, ctx, tables, hit1ID, map[string]any{"added": float64(1)})

	hit2ID := waitForHit(bp2ID)

	hits2 := listHitsForBreakpoint(t, ctx, tables, bp2ID)
	if len(hits2) != 1 {
		t.Fatalf("expected exactly 1 hit for bp2, got %d", len(hits2))
	}
	dispatchCtx, _ := hits2[0].Snapshot["dispatch_context"].(map[string]any)
	mergedInSnapshot, _ := dispatchCtx["merged_attributes"].(map[string]any)
	if mergedInSnapshot["added"] != float64(1) {
		t.Fatalf("bp2 snapshot must reflect bp1's resume overlay; got merged_attributes=%#v", mergedInSnapshot)
	}

	resumeHit(t, ctx, tables, hit2ID, nil)

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints error: %v", res.err)
		}
		if res.merged["added"] != float64(1) {
			t.Fatalf("final merged attributes missing bp1's overlay: %#v", res.merged)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after both resumes within 2s")
	}
}

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

	hits := listHitsForBreakpoint(t, ctx, tables, bpID)
	if len(hits) != 100 {
		t.Errorf("expected 100 hit rows after drop_oldest + write, got %d", len(hits))
	}
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

func TestEvaluateBreakpoints_NotifyOnlyAutoResumeTTLOverflowNeverBlocks(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowAutoResumeAfterTTL,
		HitTTLSeconds:  300,
		CreatedByKey:   "test",
	})
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
		t.Fatalf("seed 100 unresumed hits: %v", err)
	}

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}
	cc := newCheckpointContext(instanceID)

	if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
		t.Fatalf("EvaluateBreakpoints: %v", err)
	}

	hits := listHitsForBreakpoint(t, ctx, tables, bpID)
	if len(hits) != 100 {
		t.Errorf("expected the queue to stay at cap (overflow hit dropped, not queued), got %d hit rows", len(hits))
	}

	var bp *persistence.BreakpointRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		bp, err = tables.Breakpoints().Get(ctx, bpID, tx)
		return err
	}); err != nil {
		t.Fatalf("get breakpoint: %v", err)
	}
	if bp.DroppedCount < 1 {
		t.Errorf("expected dropped_count >= 1 (notify_only overflow must be dropped, not blocked), got %d", bp.DroppedCount)
	}
}

func TestEvaluateBreakpoints_OverflowAutoResumeAfterTTLSweepUnblocksRunner(t *testing.T) {
	ctx := context.Background()
	tables := openInMemoryTables(t)
	instanceID := seedBreakpointEvalFixture(t, ctx, tables)
	bpID := createBreakpointForEval(t, ctx, tables, persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowAutoResumeAfterTTL,
		HitTTLSeconds:  3600,
		CreatedByKey:   "test",
	})
	staleHitAt := time.Now().Add(-2 * time.Hour)
	hitIDs := make([]shared.UUID, 0, 100)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 100; i++ {
			hitAt := time.Now()
			if i == 0 {
				hitAt = staleHitAt
			}
			id, _, err := tables.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   instanceID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModePause,
				Snapshot:     map[string]any{"seed": i},
				HitAt:        hitAt,
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
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned while queue at cap")
	case <-time.After(400 * time.Millisecond):
	}

	if n := unresumedCountForBreakpoint(t, ctx, tables, bpID); n != 100 {
		t.Fatalf("unresumed count before sweep = %d, want 100 (nothing auto-resumed yet)", n)
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := tables.BreakpointHits().AutoResumeStale(ctx, time.Now(), tx)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("AutoResumeStale resumed %d rows, want 1 (only hitIDs[0] is past its TTL)", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("AutoResumeStale: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var newHitID shared.UUID
	for time.Now().Before(deadline) {
		hits := listHitsForBreakpoint(t, ctx, tables, bpID)
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
		t.Fatalf("evaluator never wrote its own hit row after the TTL sweep freed capacity")
	}
	resumeHit(t, ctx, tables, newHitID, nil)

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("EvaluateBreakpoints err: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("EvaluateBreakpoints did not return after the sweep + final resume")
	}

	var stale *persistence.BreakpointHitRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		stale, err = tables.BreakpointHits().Get(ctx, hitIDs[0], tx)
		return err
	}); err != nil {
		t.Fatalf("get swept hit: %v", err)
	}
	if stale.ResumedAt == nil {
		t.Fatalf("hitIDs[0] (past its TTL) must be resumed by the sweep")
	}
	if stale.ResumedByKey == nil || *stale.ResumedByKey != "sweeper" {
		t.Fatalf("hitIDs[0].ResumedByKey = %v, want %q", stale.ResumedByKey, "sweeper")
	}
}

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
	go func() {
		out, err := runtime.EvaluateBreakpoints(ctx, args, cc)
		resultCh <- evalResult{merged: out, err: err}
	}()

	select {
	case <-resultCh:
		t.Fatalf("EvaluateBreakpoints returned while queue at cap")
	case <-time.After(400 * time.Millisecond):
	}

	resumeHit(t, ctx, tables, hitIDs[0], nil)

	deadline := time.Now().Add(2 * time.Second)
	var newHitID shared.UUID
	for time.Now().Before(deadline) {
		hits := listHitsForBreakpoint(t, ctx, tables, bpID)
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

func unresumedCountForBreakpoint(t *testing.T, ctx context.Context, tables persistence.Tables, bpID shared.UUID) int {
	t.Helper()
	var n int
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		n, err = tables.BreakpointHits().UnresumedCount(ctx, bpID, tx)
		return err
	}); err != nil {
		t.Fatalf("unresumed count: %v", err)
	}
	return n
}

func TestEvaluateBreakpoints_ConcurrentEvaluatorsNeverExceedCap(t *testing.T) {
	const queueCap = 100
	const racers = 12

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

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < queueCap-1; i++ {
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
		t.Fatalf("seed %d unresumed hits: %v", queueCap-1, err)
	}

	if got := unresumedCountForBreakpoint(t, ctx, tables, bpID); got != queueCap-1 {
		t.Fatalf("precondition: unresumed=%d want %d", got, queueCap-1)
	}

	args := runtime.RunArgs{Persist: tables, Logger: shared.SilentLogger{}}

	start := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	errs := make(chan error, racers)
	ready.Add(racers)
	done.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer done.Done()
			cc := newCheckpointContext(instanceID)
			ready.Done()
			<-start
			if _, err := runtime.EvaluateBreakpoints(ctx, args, cc); err != nil {
				errs <- err
			}
		}()
	}

	ready.Wait()
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("EvaluateBreakpoints under race: %v", err)
	}

	got := unresumedCountForBreakpoint(t, ctx, tables, bpID)
	if got > queueCap {
		t.Fatalf("unresumed hit count exceeded cap: got %d want <= %d", got, queueCap)
	}
	if got != queueCap {
		t.Fatalf("drop_oldest should hold the queue at cap: got %d want %d", got, queueCap)
	}
}

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

type failingHitsTables struct {
	persistence.Tables
	hits *failingHits
}

func (f *failingHitsTables) BreakpointHits() persistence.BreakpointHitTable {
	return f.hits
}

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
