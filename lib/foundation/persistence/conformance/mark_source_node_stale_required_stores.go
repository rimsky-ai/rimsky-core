// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testMarkSourceNodeStaleCarriesRequiredClaimProducers(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	nodeID := uuid.New()
	mainRunScopeID := uuid.New()

	tmplSpec := spec.TemplateSpec{
		Name:    "mark-source-node-stale-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{
				Type:     "source-node-with-claim-producer",
				Executor: "test-executor",
				ClaimProducers: []spec.NodeClaimProducerRef{
					{Name: "fixture-claim-producer", Selector: "@root", Intent: "read"},
				},
			},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplSpec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         shared.UUID(mainRunScopeID),
			GraphName:  spec.MainGraphName,
			InstanceID: shared.UUID(instanceID),
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   "source-node-with-claim-producer",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed template/instance/node: %v", err)
	}

	messageID := uuid.New()
	var frameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(messageID),
			InstanceID: shared.UUID(instanceID),
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertRunningFrame(ctx, instanceID, shared.UUID(messageID), shared.UUID(mainRunScopeID), tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}); err != nil {
		t.Fatalf("seed frame: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		transitioned, err := store.Frames().MarkSourceNodeStale(ctx, instanceID, nodeID, frameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkSourceNodeStale: no run row was inserted")
		}
		return nil
	}); err != nil {
		t.Fatalf("MarkSourceNodeStale: %v", err)
	}

	q := d.Queue()
	assertRequiredClaimProducers := func(accepted []string) []persistence.Candidate {
		var cands []persistence.Candidate
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			cands, err = q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
				AcceptedExecutors:      []string{"test-executor"},
				AcceptedClaimProducers: accepted,
				Limit:                  16,
			})
			return err
		}); err != nil {
			t.Fatalf("SelectCandidates(accepted=%v): %v", accepted, err)
		}
		return cands
	}

	if got := assertRequiredClaimProducers([]string{}); candidateForNode(got, nodeID) != nil {
		t.Fatalf("SelectCandidates surfaced a source-node run requiring 'fixture-claim-producer' to a supervisor accepting none: %+v", got)
	}

	got := assertRequiredClaimProducers([]string{"fixture-claim-producer"})
	cand := candidateForNode(got, nodeID)
	if cand == nil {
		t.Fatalf("SelectCandidates(accepted=[fixture-claim-producer]) did not surface the source-node run: %+v", got)
	}
	if len(cand.RequiredClaimProducers) != 1 || cand.RequiredClaimProducers[0] != "fixture-claim-producer" {
		t.Fatalf("Candidate.RequiredClaimProducers = %v, want [fixture-claim-producer]", cand.RequiredClaimProducers)
	}
}

func candidateForNode(cands []persistence.Candidate, nodeID uuid.UUID) *persistence.Candidate {
	for i := range cands {
		if cands[i].NodeID == shared.UUID(nodeID) {
			return &cands[i]
		}
	}
	return nil
}
