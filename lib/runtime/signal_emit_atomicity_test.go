// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type failingEventsTable struct {
	persistence.EventTable
	fail bool
}

func (f *failingEventsTable) Append(ctx context.Context, in persistence.EventAppendInput, tx persistence.Tx) error {
	if f.fail {
		return errors.New("injected audit-append failure")
	}
	return f.EventTable.Append(ctx, in, tx)
}

type faultingEventsTables struct {
	persistence.Tables
	failEvents bool
}

func (f *faultingEventsTables) Events() persistence.EventTable {
	return &failingEventsTable{EventTable: f.Tables.Events(), fail: f.failEvents}
}

func seedSignalAtomicityFixture(t *testing.T) (tables persistence.Tables, senderNodeID, receiverNodeID, senderRunID, instanceID, frameID shared.UUID) {
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
	tables = d.Tables()
	q := d.Queue()

	templateHash := "sha256-" + uuid.NewString()
	instanceID = shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	senderNodeID = shared.UUID(uuid.New())
	receiverNodeID = shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name:    "signal-atomicity-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "sender", Executor: "test-executor"},
			{
				Type: "receiver", Executor: "test-executor",
				Subscribes: []spec.SubscriptionEntry{
					{Node: "sender", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
				},
			},
		},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: senderNodeID, InstanceID: instanceID, NodeType: "sender", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: receiverNodeID, InstanceID: instanceID, NodeType: "receiver", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 senderNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == senderNodeID {
				senderRunID = c.NodeRunID
			}
		}
		if senderRunID == (shared.UUID{}) {
			t.Fatalf("seedSignalAtomicityFixture: candidate not surfaced for sender %s", senderNodeID)
		}
		claimed, err := q.ClaimDispatchRow(ctx, senderRunID, "sup-signal-atomicity", tx)
		if err != nil {
			return err
		}
		if !claimed {
			t.Fatalf("seedSignalAtomicityFixture: run %s not claimable", senderRunID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, senderRunID, "sup-signal-atomicity", tx)
		if err != nil {
			return err
		}
		if !promoted {
			t.Fatalf("seedSignalAtomicityFixture: run %s not promoted to running", senderRunID)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	return tables, senderNodeID, receiverNodeID, senderRunID, instanceID, frameID
}

func TestEmitSignalInTx_CascadeStagingAndAuditAreBothOrNeitherAtomic(t *testing.T) {
	realTables, senderNodeID, receiverNodeID, senderRunID, instanceID, frameID := seedSignalAtomicityFixture(t)
	ctx := context.Background()

	faulting := &faultingEventsTables{Tables: realTables, failEvents: true}
	args := RunArgs{
		Persist:      faulting,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-signal-atomicity",
	}
	sig := signalpkg.BuildTerminalSuccessSignal(true, nil, "atomicity-check", nil)

	err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return emitSignalInTxOnce(ctx, args, senderNodeID, "sender", senderRunID, instanceID, frameID, sig, tx)
	})
	if err == nil {
		t.Fatalf("expected the injected audit-append failure to propagate out of emitSignalInTxOnce")
	}

	var receiverRunAfterFailure *persistence.NodeRunLatest
	if err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := realTables.Nodes().GetLatestRunForNode(ctx, receiverNodeID, tx)
		receiverRunAfterFailure = r
		return err
	}); err != nil {
		t.Fatalf("query receiver run after failure: %v", err)
	}
	if receiverRunAfterFailure != nil {
		t.Fatalf("cascade staging for the receiver must not survive when the audit half of the same "+
			"transaction fails; found a receiver run (state=%s), want none", receiverRunAfterFailure.State)
	}

	var auditCountAfterFailure int
	if err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := realTables.Events().List(ctx, persistence.EventListFilter{InstanceID: &instanceID}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		auditCountAfterFailure = len(rows.Events)
		return nil
	}); err != nil {
		t.Fatalf("query events after failure: %v", err)
	}
	if auditCountAfterFailure != 0 {
		t.Fatalf("no audit event may survive when the failing audit-append itself rolls back the "+
			"whole transaction; got %d event rows, want 0", auditCountAfterFailure)
	}

	nonFaulting := RunArgs{
		Persist:      realTables,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-signal-atomicity",
	}
	if err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return emitSignalInTxOnce(ctx, nonFaulting, senderNodeID, "sender", senderRunID, instanceID, frameID, sig, tx)
	}); err != nil {
		t.Fatalf("emitSignalInTxOnce without fault injection: %v", err)
	}

	var receiverRunAfterSuccess *persistence.NodeRunLatest
	if err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := realTables.Nodes().GetLatestRunForNode(ctx, receiverNodeID, tx)
		receiverRunAfterSuccess = r
		return err
	}); err != nil {
		t.Fatalf("query receiver run after success: %v", err)
	}
	if receiverRunAfterSuccess == nil {
		t.Fatalf("cascade staging for the receiver must land once the bundled emission succeeds; " +
			"found no receiver run")
	}

	var auditCountAfterSuccess int
	if err := realTables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := realTables.Events().List(ctx, persistence.EventListFilter{InstanceID: &instanceID}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		auditCountAfterSuccess = len(rows.Events)
		return nil
	}); err != nil {
		t.Fatalf("query events after success: %v", err)
	}
	if auditCountAfterSuccess == 0 {
		t.Fatalf("the audit half must land alongside the cascade-staging half once the bundled " +
			"emission succeeds; got 0 event rows, want at least 1")
	}
}
