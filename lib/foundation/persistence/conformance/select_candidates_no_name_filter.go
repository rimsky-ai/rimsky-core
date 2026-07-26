// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: supervisor
// @concept: service-address-book
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type dispatchSeed struct {
	NodeID                 shared.UUID
	ExecutorName           string
	RequiredClaimProducers []string
}

func seedDispatchRows(
	ctx context.Context, t *testing.T, d persistence.Database,
	templateHash string, serviceBindings json.RawMessage, seeds []dispatchSeed,
) {
	t.Helper()
	store := d.Tables()
	q := d.Queue()

	instanceID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         runScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:              instanceID,
			TemplateHash:    templateHash,
			ServiceBindings: serviceBindings,
		}, tx); err != nil {
			return err
		}
		messageID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         messageID,
			InstanceID: instanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := store.Frames().InsertRunningFrame(ctx, instanceID, messageID, runScopeID, tx)
		if err != nil {
			return err
		}
		for _, s := range seeds {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID:         s.NodeID,
				InstanceID: instanceID,
				NodeType:   "fixture-node-type",
				Executor:   s.ExecutorName,
			}, tx); err != nil {
				return err
			}
			if err := q.Enqueue(ctx, persistence.DispatchRequest{
				NodeID:                 s.NodeID,
				ExecutorName:           s.ExecutorName,
				RequiredClaimProducers: s.RequiredClaimProducers,
				EnqueuedAt:             time.Now().Add(-1 * time.Second),
				FrameID:                frameID,
				RunScopeID:             runScopeID,
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seedDispatchRows: %v", err)
	}
}

func selectCandidateNodeIDs(ctx context.Context, t *testing.T, d persistence.Database, req persistence.SelectCandidatesRequest) map[shared.UUID]bool {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	probeErr := errors.New("rollback probe")
	seen := map[shared.UUID]bool{}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, req, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			seen[c.NodeID] = true
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates: %v", err)
	}
	return seen
}

func testSelectCandidatesIgnoresServiceNames(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	declaredExecutorNode := shared.UUID(uuid.New())
	unknownExecutorNode := shared.UUID(uuid.New())
	unknownStoreNode := shared.UUID(uuid.New())
	lateBoundExecutorNode := shared.UUID(uuid.New())
	seedDispatchRows(ctx, t, d, fix.TemplateHash,
		json.RawMessage(`{"proxy-executor":true}`),
		[]dispatchSeed{
			{NodeID: declaredExecutorNode, ExecutorName: "test-executor", RequiredClaimProducers: []string{"static-store"}},
			{NodeID: unknownExecutorNode, ExecutorName: "executor-nobody-declared", RequiredClaimProducers: []string{}},
			{NodeID: unknownStoreNode, ExecutorName: "test-executor", RequiredClaimProducers: []string{"store-nobody-declared"}},
			{NodeID: lateBoundExecutorNode, ExecutorName: "proxy-executor", RequiredClaimProducers: []string{}},
		},
	)

	seen := selectCandidateNodeIDs(ctx, t, d, persistence.SelectCandidatesRequest{Limit: 100})
	for name, id := range map[string]shared.UUID{
		"declared executor":   declaredExecutorNode,
		"undeclared executor": unknownExecutorNode,
		"undeclared store":    unknownStoreNode,
		"late-bound executor": lateBoundExecutorNode,
	} {
		if !seen[id] {
			t.Errorf("row with %s was not selectable — candidate selection must not filter on service names; "+
				"names resolve after acquisition against the service address book and failures surface as "+
				"unresolved-service dispatch errors, never as silent queue stalls", name)
		}
	}
}
