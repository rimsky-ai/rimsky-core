// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_emit_message_test.go — message-emitter-node dispatch and
// terminal-resolution guarantees (concept:message-emitter-node).
//
// Covers the two load-bearing properties from the spec's Falsifier:
//
//   - Envelope insert is atomic with the sender's terminal-resolution tx.
//     Forced tx-rollback after the envelope insert leaves no row in
//     `rimsky_messages` — `TestEmitCascadeMessageInTx_RollbackAtomic`.
//
//   - Idempotency on cascade-emit is deterministic on `(node_id,
//     frame_id)`. Two invocations of the helper with the same
//     (node_id, frame_id) pair produce exactly one envelope row, and
//     the second call returns the prior id with `replayed=true` —
//     `TestEmitCascadeMessageInTx_IdempotentOnNodeAndFrame`. Keying on
//     the run-row id was the prior shape; it would duplicate envelopes
//     on supervisor-side hard-failure re-enqueue (fresh run id each
//     time).

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// openEmitTestDB opens a fresh sqlite-backed persistence.Database, runs
// migrations, and registers cleanup so the test isolates from siblings.
func openEmitTestDB(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "emit.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// seedEmitInstance seeds a minimal template + run-scope + instance so
// the messages.instance_id FK validates on Insert. Returns the
// instance id.
func seedEmitInstance(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:           "emit-message-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		// @deliberate: declare the destination message type so receivers
		// / cross-checks would find it; the helper under test does not
		// consult the template at all (it relies on the validator
		// having already approved the shape) but seeding a realistic
		// shape keeps the fixture self-documenting.
		Messages: []spec.MessageSchema{{
			Type: "ping/recheck",
			BodySchema: []byte(`{
				"type": "object",
				"properties": { "pong_status": {"type": "string"} },
				"required": ["pong_status"]
			}`),
		}},
		Nodes: []spec.TemplateNodeDef{
			{Type: "n", EmitsMessage: "ping/recheck"},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedEmitInstance: %v", err)
	}
	return instanceID
}

// countMessages returns the total number of message rows persisted for
// the given instance, irrespective of delivery state. Reads through the
// tables' List path so the assertion sees what an operator-facing
// observability surface would see.
func countMessages(t *testing.T, ctx context.Context, d persistence.Database, instanceID shared.UUID) int {
	t.Helper()
	page, err := d.Tables().Messages().List(ctx,
		persistence.MessageListFilter{InstanceID: &instanceID},
		persistence.ListPagination{Limit: 256})
	if err != nil {
		t.Fatalf("Messages.List: %v", err)
	}
	return len(page.Rows)
}

// TestEmitCascadeMessageInTx_HappyPath covers the green-path: helper
// inserts the envelope with the expected fields and returns
// (newID, false).
func TestEmitCascadeMessageInTx_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			map[string]any{"pong_status": "needs_work"})
		if err != nil {
			return err
		}
		if replayed {
			t.Errorf("first emit must not be a replay")
		}
		msgID = id
		return nil
	}); err != nil {
		t.Fatalf("emit tx: %v", err)
	}

	if msgID == (shared.UUID{}) {
		t.Fatalf("expected a non-zero message id, got zero")
	}
	row, err := d.Tables().Messages().Get(ctx, msgID)
	if err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("expected envelope to exist after commit, got nil")
	}
	if row.Type != "ping/recheck" {
		t.Errorf("Type = %q; want ping/recheck", row.Type)
	}
	if row.SenderKind != "instance" {
		t.Errorf("SenderKind = %q; want instance", row.SenderKind)
	}
	wantSender := "instance:" + instanceID.String()
	if row.Sender != wantSender {
		t.Errorf("Sender = %q; want %q", row.Sender, wantSender)
	}
	var body map[string]any
	if err := json.Unmarshal(row.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v (payload=%q)", err, string(row.Payload))
	}
	if body["pong_status"] != "needs_work" {
		t.Errorf("payload.pong_status = %v; want needs_work", body["pong_status"])
	}
}

// TestEmitCascadeMessageInTx_RollbackAtomic pins the load-bearing
// property: a tx-rollback AFTER the envelope insert (but before the
// caller's tx commits) MUST leave no row in rimsky_messages. The helper
// runs inside the caller's outer tx; rollback rolls the envelope back.
func TestEmitCascadeMessageInTx_RollbackAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	preCount := countMessages(t, ctx, d, instanceID)

	// @deliberate: run the emit inside a tx that returns a synthetic
	// error, forcing the persistence layer to roll back. The returned
	// message id IS populated inside the tx; that part of the helper
	// succeeded. The load-bearing claim is that the rollback unmakes
	// the envelope.
	sentinelErr := errors.New("forced rollback after emit")
	err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, emitErr := emitCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			map[string]any{"pong_status": "ok"})
		if emitErr != nil {
			return emitErr
		}
		if replayed {
			t.Errorf("first emit must not be a replay")
		}
		if id == (shared.UUID{}) {
			t.Errorf("expected a non-zero candidate id inside the tx")
		}
		// @deliberate: caller decides to abort — atomicity claim is
		// that the prior insert is undone.
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected sentinel error to propagate, got %v", err)
	}

	postCount := countMessages(t, ctx, d, instanceID)
	if postCount != preCount {
		t.Fatalf("envelope row count = %d after rollback; want %d (rollback must unmake the emit)", postCount, preCount)
	}
}

// TestEmitCascadeMessageInTx_IdempotentOnNodeAndFrame pins the second
// load-bearing property: two invocations with the same (node_id,
// frame_id) pair produce exactly one envelope, and the second
// invocation returns the prior id with replayed=true.
//
// This is the retry-resistance property. The supervisor's
// infra-error re-enqueue path mints a FRESH `rimsky_node_runs.id` on
// every retry (the prior key shape `cascade-emit:<dispatch_id>` would
// therefore duplicate envelopes), but `(node_id, frame_id)` is
// unchanged across the re-enqueue — the deterministic Idempotency-Key
// `cascade-emit:<node_id>:<frame_id>` collapses the retry onto the
// dedup row from the first attempt.
func TestEmitCascadeMessageInTx_IdempotentOnNodeAndFrame(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var firstID, secondID shared.UUID
	var firstReplayed, secondReplayed bool

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			map[string]any{"pong_status": "v1"})
		if err != nil {
			return err
		}
		firstID = id
		firstReplayed = replayed
		return nil
	}); err != nil {
		t.Fatalf("first emit tx: %v", err)
	}

	if firstReplayed {
		t.Errorf("first emit must not be a replay")
	}

	// @constraint: retry with the same (node_id, frame_id) and a
	// DIFFERENT body — replay must return the original id, NOT insert a
	// second envelope. The dedup tuple ignores the payload by design;
	// idempotency keys identify the logical operation, not the data
	// carried.
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			map[string]any{"pong_status": "v2-IGNORED"})
		if err != nil {
			return err
		}
		secondID = id
		secondReplayed = replayed
		return nil
	}); err != nil {
		t.Fatalf("second emit tx: %v", err)
	}

	if !secondReplayed {
		t.Errorf("second emit with same (node_id, frame_id) must be a replay; got replayed=false")
	}
	if firstID != secondID {
		t.Errorf("replayed id = %s; want first id %s", secondID.String(), firstID.String())
	}

	// @constraint: net effect — exactly one envelope row for this instance.
	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope row count = %d after retry; want exactly 1 (deterministic idempotency on (node_id, frame_id))", got)
	}

	// @constraint: the persisted payload is the FIRST emit's payload,
	// not the retry's — the retry must not overwrite the prior body.
	row, err := d.Tables().Messages().Get(ctx, firstID)
	if err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("expected envelope row, got nil")
	}
	var body map[string]any
	if err := json.Unmarshal(row.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if body["pong_status"] != "v1" {
		t.Errorf("payload.pong_status = %v; want v1 (replay must not overwrite the prior body)", body["pong_status"])
	}
}

// TestEmitCascadeMessageInTx_EnqueuesFrameForEnvelope pins the
// load-bearing wiring property: a cascade emit must enqueue a queued
// frame whose `triggering_message_id` equals the newly-inserted
// envelope's id. Without this frame, `SweepDeliverMessagesForRunningFrames`
// would never deliver the envelope (it only iterates running frames and
// never creates new ones), and the next-frame subscribers would never
// wake — STORY-cascade-emit / STORY-cross-frame-coupling /
// STORY-one-message-per-frame would all fail.
func TestEmitCascadeMessageInTx_EnqueuesFrameForEnvelope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			map[string]any{"pong_status": "needs_work"})
		if err != nil {
			return err
		}
		if replayed {
			t.Errorf("first emit must not be a replay")
		}
		msgID = id
		return nil
	}); err != nil {
		t.Fatalf("emit tx: %v", err)
	}

	// @constraint: there must be exactly one queued frame for this
	// instance pointing at the new envelope. Read via
	// ListForObservability with the triggering-message filter to
	// confirm the FK was set correctly.
	var page persistence.PaginatedListResult[persistence.FrameRow]
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := d.Tables().Frames().ListForObservability(ctx,
			persistence.FrameListFilter{
				InstanceID:          &instanceID,
				TriggeringMessageID: &msgID,
			},
			persistence.ListPagination{Limit: 16}, tx)
		page = p
		return err
	}); err != nil {
		t.Fatalf("Frames.ListForObservability: %v", err)
	}
	if got := len(page.Rows); got != 1 {
		t.Fatalf("frame count for triggering_message_id = %d; want exactly 1 (emit must enqueue exactly one frame for its envelope)", got)
	}
	got := page.Rows[0]
	if got.TriggeringMessageID != msgID {
		t.Errorf("frame.triggering_message_id = %s; want %s", got.TriggeringMessageID.String(), msgID.String())
	}
	if got.InstanceID != instanceID {
		t.Errorf("frame.instance_id = %s; want %s", got.InstanceID.String(), instanceID.String())
	}
	if got.State != persistence.FrameStateQueued {
		t.Errorf("frame.state = %q; want queued (emit enqueues the frame; promotion happens later in the scheduler tick)", got.State)
	}
}

// TestEmitCascadeMessageInTx_ReplayDoesNotDoubleEnqueueFrame pins the
// retry-safety property for the new frame-enqueue wiring: a retried
// emit (same (node_id, frame_id)) returns `replayed=true` and MUST NOT
// enqueue a second frame for the same envelope. The prior emit's frame
// still points at the same envelope; doubling the frame count on retry
// would break STORY-one-message-per-frame (the retry-induced second
// frame would carry no second envelope, just promote and find nothing
// to deliver).
func TestEmitCascadeMessageInTx_ReplayDoesNotDoubleEnqueueFrame(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	for i := 0; i < 2; i++ {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
				instanceID, nodeID, frameID, "ping/recheck",
				map[string]any{"pong_status": "v"})
			return err
		}); err != nil {
			t.Fatalf("emit iter %d: %v", i, err)
		}
	}

	// @constraint: exactly one envelope from the idempotency property,
	// exactly one frame from this fix's load-bearing property.
	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope count = %d; want 1 (replay dedups envelope)", got)
	}
	var page persistence.PaginatedListResult[persistence.FrameRow]
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := d.Tables().Frames().ListForObservability(ctx,
			persistence.FrameListFilter{InstanceID: &instanceID},
			persistence.ListPagination{Limit: 16}, tx)
		page = p
		return err
	}); err != nil {
		t.Fatalf("Frames.ListForObservability: %v", err)
	}
	if got := len(page.Rows); got != 1 {
		t.Fatalf("frame count = %d; want exactly 1 (replay must not enqueue a second frame)", got)
	}
}

// TestEmitCascadeMessageInTx_DistinctNodeFramePairProducesDistinctEnvelopes
// is the complement to the idempotency test: two emits from DIFFERENT
// (node_id, frame_id) pairs produce TWO envelopes. This pins that the
// dedup is scoped on (node_id, frame_id) (and on the cascade-emit
// prefix) — two independent emits from the same instance must not
// collide.
func TestEmitCascadeMessageInTx_DistinctNodeFramePairProducesDistinctEnvelopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)

	for i := 0; i < 3; i++ {
		nodeID := shared.UUID(uuid.New())
		frameID := shared.UUID(uuid.New())
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := emitCascadeMessageInTx(ctx, d.Tables(), tx,
				instanceID, nodeID, frameID, "ping/recheck",
				map[string]any{"pong_status": "v"})
			return err
		}); err != nil {
			t.Fatalf("emit iter %d: %v", i, err)
		}
	}
	if got := countMessages(t, ctx, d, instanceID); got != 3 {
		t.Fatalf("envelope row count = %d; want 3 (distinct (node_id, frame_id) pairs must produce distinct envelopes)", got)
	}
}
