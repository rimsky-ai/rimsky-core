// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func seedMessageDeliveryFixture(t *testing.T) (persistence.Tables, shared.UUID, shared.UUID) {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	namedNodeID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "message-delivery-empty-wake-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "named-node", Executor: "test-executor"},
		},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: namedNodeID, InstanceID: instanceID, NodeType: "named-node", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	return tables, instanceID, namedNodeID
}

func hasRunInFrame(t *testing.T, tables persistence.Tables, nodeID, frameID shared.UUID) bool {
	t.Helper()
	ctx := context.Background()
	var has bool
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		h, err := tables.Nodes().HasRunForNodeInFrame(ctx, nodeID, frameID, tx)
		has = h
		return err
	}); err != nil {
		t.Fatalf("HasRunForNodeInFrame: %v", err)
	}
	return has
}

func TestFindMessageReceiverNode_EmptyTypeUnifiedWithUnmatchedType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tables, instanceID, _ := seedMessageDeliveryFixture(t)

	var emptyReceiver, unmatchedReceiver *persistence.NodeRow
	var emptyErr, unmatchedErr error
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		emptyReceiver, emptyErr = findMessageReceiverNode(ctx, tables, instanceID, "", tx)
		unmatchedReceiver, unmatchedErr = findMessageReceiverNode(ctx, tables, instanceID, "totally-unregistered-type", tx)
		return nil
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}

	if emptyErr != nil {
		t.Fatalf("findMessageReceiverNode(type=\"\") returned error: %v", emptyErr)
	}
	if unmatchedErr != nil {
		t.Fatalf("findMessageReceiverNode(type=unmatched) returned error: %v", unmatchedErr)
	}
	if emptyReceiver != nil {
		t.Fatalf("findMessageReceiverNode(type=\"\") found a receiver node %+v; "+
			"no template ever declares a node with an empty type, and the empty case must be "+
			"resolved through the exact same lookup-by-type miss as any unregistered type, not "+
			"a dedicated empty-type branch", emptyReceiver)
	}
	if unmatchedReceiver != nil {
		t.Fatalf("findMessageReceiverNode(type=unmatched) found a receiver node %+v unexpectedly", unmatchedReceiver)
	}
}

func TestDeliverNamedMessageInTx_EmptyTypeIsANoOpDeadLetterLikeAnyUnmatchedType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	run := func(t *testing.T, msgType string) bool {
		t.Helper()
		tables, instanceID, namedNodeID := seedMessageDeliveryFixture(t)
		frameID := shared.UUID(uuid.New())
		if before := hasRunInFrame(t, tables, namedNodeID, frameID); before {
			t.Fatalf("fixture must start with no run for named-node in the fresh frame")
		}

		msg := persistence.MessageRow{
			ID:         shared.UUID(uuid.New()),
			InstanceID: instanceID,
			Type:       msgType,
			Sender:     "test",
			SenderKind: "operator",
		}
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return deliverNamedMessageInTx(ctx, tables, shared.SilentLogger{}, instanceID, frameID, msg, shared.UUID(uuid.New()), time.Now(), tx)
		}); err != nil {
			t.Fatalf("deliverNamedMessageInTx(type=%q): %v", msgType, err)
		}
		return hasRunInFrame(t, tables, namedNodeID, frameID)
	}

	emptyCreatedRun := run(t, "")
	unmatchedCreatedRun := run(t, "totally-unregistered-type")

	if emptyCreatedRun {
		t.Fatalf("deliverNamedMessageInTx(type=\"\") created a node-run for named-node; the " +
			"empty-type message must dead-letter through the same no-receiver-found path as any " +
			"unmatched type, not a dedicated empty-type branch that creates side effects")
	}
	if unmatchedCreatedRun {
		t.Fatalf("deliverNamedMessageInTx(type=unmatched) created a node-run for named-node unexpectedly")
	}
	if emptyCreatedRun != unmatchedCreatedRun {
		t.Fatalf("empty-type and unmatched-type delivery must produce identical outcomes "+
			"(run created: empty=%v, unmatched=%v) -- any divergence means a dedicated "+
			"empty-type branch has been introduced into message_delivery.go", emptyCreatedRun, unmatchedCreatedRun)
	}
}
