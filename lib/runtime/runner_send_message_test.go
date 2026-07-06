// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		Messages: []spec.MessageSchema{{
			Type: "ping/recheck",
			BodySchema: []byte(`{
				"type": "object",
				"properties": { "pong_status": {"type": "string"} },
				"required": ["pong_status"]
			}`),
		}},
		Nodes: []spec.TemplateNodeDef{
			{Type: "n", SendsMessage: "ping/recheck"},
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
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedEmitInstance: %v", err)
	}
	return instanceID
}

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

func TestSendCascadeMessageInTx_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			[]byte(`{"pong_status":"needs_work"}`))
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

func TestSendCascadeMessageInTx_RollbackAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	preCount := countMessages(t, ctx, d, instanceID)

	sentinelErr := errors.New("forced rollback after emit")
	err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, emitErr := sendCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			[]byte(`{"pong_status":"ok"}`))
		if emitErr != nil {
			return emitErr
		}
		if replayed {
			t.Errorf("first emit must not be a replay")
		}
		if id == (shared.UUID{}) {
			t.Errorf("expected a non-zero candidate id inside the tx")
		}
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

func TestSendCascadeMessageInTx_IdempotentOnNodeAndFrame(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var firstID, secondID shared.UUID
	var firstReplayed, secondReplayed bool

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			[]byte(`{"pong_status":"v1"}`))
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

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			[]byte(`{"pong_status":"v2-IGNORED"}`))
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

	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope row count = %d after retry; want exactly 1 (deterministic idempotency on (node_id, frame_id))", got)
	}

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

func TestSendCascadeMessageInTx_InsertsMessageEnvelope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
			instanceID, nodeID, frameID, "ping/recheck",
			[]byte(`{"pong_status":"needs_work"}`))
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

	row, err := d.Tables().Messages().Get(ctx, msgID)
	if err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	if row == nil {
		t.Fatalf("expected message envelope for id=%s; got nil", msgID)
	}
	if row.InstanceID != instanceID {
		t.Errorf("message.instance_id = %s; want %s", row.InstanceID, instanceID)
	}
	if row.DeliveredAt != nil {
		t.Errorf("message.delivered_at = %v; want nil (frame opens later, in the tick)", row.DeliveredAt)
	}
}

func TestSendCascadeMessageInTx_ReplayDoesNotDoubleInsertEnvelope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	for i := 0; i < 2; i++ {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
				instanceID, nodeID, frameID, "ping/recheck",
				[]byte(`{"pong_status":"v"}`))
			return err
		}); err != nil {
			t.Fatalf("emit iter %d: %v", i, err)
		}
	}

	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope count = %d; want 1 (replay dedups envelope)", got)
	}
}

func TestSendCascadeMessageInTx_DistinctNodeFramePairProducesDistinctEnvelopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openEmitTestDB(t)
	instanceID := seedEmitInstance(t, ctx, d)

	for i := 0; i < 3; i++ {
		nodeID := shared.UUID(uuid.New())
		frameID := shared.UUID(uuid.New())
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := sendCascadeMessageInTx(ctx, d.Tables(), tx,
				instanceID, nodeID, frameID, "ping/recheck",
				[]byte(`{"pong_status":"v"}`))
			return err
		}); err != nil {
			t.Fatalf("emit iter %d: %v", i, err)
		}
	}
	if got := countMessages(t, ctx, d, instanceID); got != 3 {
		t.Fatalf("envelope row count = %d; want 3 (distinct (node_id, frame_id) pairs must produce distinct envelopes)", got)
	}
}
