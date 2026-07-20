// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: supervisor
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

type lateBindDispatchSeed struct {
	NodeID                 shared.UUID
	ExecutorName           string
	RequiredClaimProducers []string
}

func seedLateBindDispatch(
	ctx context.Context, t *testing.T, d persistence.Database,
	templateHash string, serviceBindings json.RawMessage, seeds []lateBindDispatchSeed,
) {
	t.Helper()
	store := d.Tables()
	q := d.Queue()

	instanceID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         runScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:              instanceID,
			TemplateHash:    templateHash,
			ServiceBindings: serviceBindings,
		}, tx); err != nil {
			return err
		}
		messageID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         messageID,
			InstanceID: instanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
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
			if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
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
		t.Fatalf("seedLateBindDispatch: %v", err)
	}
}

func selectCandidateNodeIDs(ctx context.Context, t *testing.T, d persistence.Database, req persistence.SelectCandidatesRequest) map[shared.UUID]bool {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	probeErr := errors.New("rollback probe")
	seen := map[shared.UUID]bool{}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, req)
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

func testSelectCandidatesLateBindMixedStores(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	boundNode := shared.UUID(uuid.New())
	unboundNode := shared.UUID(uuid.New())
	seedLateBindDispatch(ctx, t, d, fix.TemplateHash,
		json.RawMessage(`{"proxy-store-1":true}`),
		[]lateBindDispatchSeed{
			{NodeID: boundNode, ExecutorName: "test-executor", RequiredClaimProducers: []string{"static-store", "proxy-store-1"}},
			{NodeID: unboundNode, ExecutorName: "test-executor", RequiredClaimProducers: []string{"static-store", "proxy-store-2"}},
		},
	)

	seen := selectCandidateNodeIDs(ctx, t, d, persistence.SelectCandidatesRequest{
		AcceptedExecutors:          []string{"test-executor"},
		AcceptedClaimProducers:     []string{"static-store", "svc-store-proxy"},
		LateBindClaimProducerProxy: "svc-store-proxy",
		Limit:                      100,
	})
	if !seen[boundNode] {
		t.Errorf("node requiring a mix of statically-accepted and bound late-bind stores was not selectable")
	}
	if seen[unboundNode] {
		t.Errorf("node requiring an unbound store was selectable despite no matching service binding")
	}
}

func testSelectCandidatesLateBindExecutor(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	boundNode := shared.UUID(uuid.New())
	unboundNode := shared.UUID(uuid.New())
	seedLateBindDispatch(ctx, t, d, fix.TemplateHash,
		json.RawMessage(`{"proxy-executor":true}`),
		[]lateBindDispatchSeed{
			{NodeID: boundNode, ExecutorName: "proxy-executor", RequiredClaimProducers: []string{}},
			{NodeID: unboundNode, ExecutorName: "other-proxy-executor", RequiredClaimProducers: []string{}},
		},
	)

	seen := selectCandidateNodeIDs(ctx, t, d, persistence.SelectCandidatesRequest{
		AcceptedExecutors:     []string{"svc-executor-proxy"},
		LateBindExecutorProxy: "svc-executor-proxy",
		Limit:                 100,
	})
	if !seen[boundNode] {
		t.Errorf("node whose executor is late-bound via a service binding was not selectable")
	}
	if seen[unboundNode] {
		t.Errorf("node whose executor has no matching service binding was selectable via the proxy alone")
	}
}

func testSelectCandidatesNoLateBindProxyLeavesStaticFilterUnchanged(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)

	node := shared.UUID(uuid.New())
	seedLateBindDispatch(ctx, t, d, fix.TemplateHash,
		json.RawMessage(`{"proxy-store-1":true}`),
		[]lateBindDispatchSeed{
			{NodeID: node, ExecutorName: "test-executor", RequiredClaimProducers: []string{"static-store", "proxy-store-1"}},
		},
	)

	seen := selectCandidateNodeIDs(ctx, t, d, persistence.SelectCandidatesRequest{
		AcceptedExecutors:      []string{"test-executor"},
		AcceptedClaimProducers: []string{"static-store"},
		Limit:                  100,
	})
	if seen[node] {
		t.Errorf("node requiring an unaccepted store was selectable with no late-bind proxy configured")
	}
}
