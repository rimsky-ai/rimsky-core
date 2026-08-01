// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func openSendTestDB(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "send.db")},
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

func seedSendInstance(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "send-message-fixture",
		Version: "1",
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
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedSendInstance: %v", err)
	}
	return instanceID
}

func seedSendInstanceNoBodySchema(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "send-message-no-schema-fixture",
		Version: "1",
		Messages: []spec.MessageSchema{{
			Type: "ping/unconstrained",
		}},
		Nodes: []spec.TemplateNodeDef{
			{Type: "n", SendsMessage: "ping/unconstrained"},
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
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedSendInstanceNoBodySchema: %v", err)
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
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"needs_work"}`), tx)
		if err != nil {
			return err
		}
		if replayed {
			t.Errorf("first send must not be a replay")
		}
		msgID = id
		return nil
	}); err != nil {
		t.Fatalf("send tx: %v", err)
	}

	if msgID == (shared.UUID{}) {
		t.Fatalf("expected a non-zero message id, got zero")
	}
	row := getMessageRowInTx(t, ctx, d.Tables(), msgID)
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
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	preCount := countMessages(t, ctx, d, instanceID)

	sentinelErr := errors.New("forced rollback after send")
	err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, sendErr := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"ok"}`), tx)
		if sendErr != nil {
			return sendErr
		}
		if replayed {
			t.Errorf("first send must not be a replay")
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
		t.Fatalf("envelope row count = %d after rollback; want %d (rollback must unmake the send)", postCount, preCount)
	}
}

func TestSendCascadeMessage_NotCancelledByLaterRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	msgID, replayed, err := sendCascadeMessage(ctx, d.Tables(),
		instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"ok"}`))
	if err != nil {
		t.Fatalf("sendCascadeMessage: %v", err)
	}
	if replayed {
		t.Errorf("first send must not be a replay")
	}

	row := getMessageRowInTx(t, ctx, d.Tables(), msgID)
	if row == nil {
		t.Fatalf("expected the sent envelope to be durably committed immediately, before any later transaction runs")
	}

	sentinelErr := errors.New("simulated terminal-resolution failure after the message was already sent")
	terminalErr := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return sentinelErr
	})
	if !errors.Is(terminalErr, sentinelErr) {
		t.Fatalf("expected sentinel error to propagate, got %v", terminalErr)
	}

	postRow := getMessageRowInTx(t, ctx, d.Tables(), msgID)
	if postRow == nil {
		t.Fatalf("the already-sent message must survive a later terminal-resolution transaction's rollback " +
			"(message emission is not part of the terminal-apply tx); got nil after the sentinel rollback")
	}
}

func TestSendCascadeMessageInTx_IdempotentOnNodeAndFrame(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var firstID, secondID shared.UUID
	var firstReplayed, secondReplayed bool

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"v1"}`), tx)
		if err != nil {
			return err
		}
		firstID = id
		firstReplayed = replayed
		return nil
	}); err != nil {
		t.Fatalf("first send tx: %v", err)
	}

	if firstReplayed {
		t.Errorf("first send must not be a replay")
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"v2-IGNORED"}`), tx)
		if err != nil {
			return err
		}
		secondID = id
		secondReplayed = replayed
		return nil
	}); err != nil {
		t.Fatalf("second send tx: %v", err)
	}

	if !secondReplayed {
		t.Errorf("second send with same (node_id, frame_id) must be a replay; got replayed=false")
	}
	if firstID != secondID {
		t.Errorf("replayed id = %s; want first id %s", secondID.String(), firstID.String())
	}

	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope row count = %d after retry; want exactly 1 (deterministic idempotency on (node_id, frame_id))", got)
	}

	row := getMessageRowInTx(t, ctx, d.Tables(), firstID)
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
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, replayed, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"needs_work"}`), tx)
		if err != nil {
			return err
		}
		if replayed {
			t.Errorf("first send must not be a replay")
		}
		msgID = id
		return nil
	}); err != nil {
		t.Fatalf("send tx: %v", err)
	}

	row := getMessageRowInTx(t, ctx, d.Tables(), msgID)
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
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	for i := 0; i < 2; i++ {
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"v"}`), tx)
			return err
		}); err != nil {
			t.Fatalf("send iter %d: %v", i, err)
		}
	}

	if got := countMessages(t, ctx, d, instanceID); got != 1 {
		t.Fatalf("envelope count = %d; want 1 (replay dedups envelope)", got)
	}
}

func TestSendCascadeMessageInTx_EmptyNonNilBodyDefaultsToJSONObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSendTestDB(t)
	instanceID := seedSendInstanceNoBodySchema(t, ctx, d)
	nodeID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	var msgID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/unconstrained", []byte{}, tx)
		msgID = id
		return err
	}); err != nil {
		t.Fatalf("send tx: %v", err)
	}

	row := getMessageRowInTx(t, ctx, d.Tables(), msgID)
	if row == nil {
		t.Fatalf("expected envelope row, got nil")
	}
	var body map[string]any
	if err := json.Unmarshal(row.Payload, &body); err != nil {
		t.Fatalf("an empty non-nil body must be defaulted to a valid JSON object, "+
			"got unparseable payload %q: %v", string(row.Payload), err)
	}
	if len(body) != 0 {
		t.Errorf("payload = %v; want empty object", body)
	}
}

func TestSendCascadeMessageInTx_DistinctNodeFramePairProducesDistinctEnvelopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSendTestDB(t)
	instanceID := seedSendInstance(t, ctx, d)

	for i := 0; i < 3; i++ {
		nodeID := shared.UUID(uuid.New())
		frameID := shared.UUID(uuid.New())
		if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := sendCascadeMessageInTx(ctx, d.Tables(), instanceID, nodeID, frameID, "ping/recheck", []byte(`{"pong_status":"v"}`), tx)
			return err
		}); err != nil {
			t.Fatalf("send iter %d: %v", i, err)
		}
	}
	if got := countMessages(t, ctx, d, instanceID); got != 3 {
		t.Fatalf("envelope row count = %d; want 3 (distinct (node_id, frame_id) pairs must produce distinct envelopes)", got)
	}
}

func getMessageRowInTx(t *testing.T, ctx context.Context, tables persistence.Tables, id shared.UUID) *persistence.MessageRow {
	t.Helper()
	var row *persistence.MessageRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Messages().Get(ctx, id, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("Messages.Get: %v", err)
	}
	return row
}
