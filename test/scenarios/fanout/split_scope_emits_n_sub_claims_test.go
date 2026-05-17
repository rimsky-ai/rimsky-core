// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N2 scenario — split_scope_emits_n_sub_claims.
//
// At fan-out acquisition the supervisor calls ClaimProducer.SplitScope
// and INSERTs one rimsky_claim_handles row per returned sub-scope
// descriptor (with parent_claim_handle_id pointing at the parent).
// Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Fan-out template DSL "Mechanics at dispatch" steps 2-3.
//
// This scenario validates the `PlanFanOutChildren` projection — the
// pure helper that turns N sub-claim descriptors into N child-run
// plans the dispatcher feeds to CreateChildRun. The unit-level
// coverage in runtime/fanout_dispatch_test.go pins the projection
// rules; this scenario exercises the additional contract that the
// per-child plan carries the leaf executor + sub-claim handle id +
// partition_key one-to-one.
package fanout

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

func TestSplitScopeEmitsNSubClaims_PlanProjectsOneChildPerSubScope(t *testing.T) {
	t.Parallel()
	parentRun := shared.UUID(uuid.New())
	parentNode := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())

	// Simulate the producer-returned sub-scope list: 5 partitions.
	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-east-1", Address: json.RawMessage(`{"path":"a"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-west-2", Address: json.RawMessage(`{"path":"b"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "eu-west-1", Address: json.RawMessage(`{"path":"c"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "ap-south-1", Address: json.RawMessage(`{"path":"d"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "sa-east-1", Address: json.RawMessage(`{"path":"e"}`)},
	}
	plans := runtime.PlanFanOutChildren(parentRun, parentNode, frameID, subClaims,
		"my-loader", []string{"parquet-store"})
	if len(plans) != len(subClaims) {
		t.Fatalf("plans: %d (want %d, one per sub-claim)", len(plans), len(subClaims))
	}
	for i, p := range plans {
		if p.ParentRunID != parentRun {
			t.Errorf("plans[%d].ParentRunID: %s (want %s)", i, p.ParentRunID, parentRun)
		}
		if p.NodeID != parentNode {
			t.Errorf("plans[%d].NodeID: %s (want %s)", i, p.NodeID, parentNode)
		}
		if p.FrameID != frameID {
			t.Errorf("plans[%d].FrameID: %s (want %s)", i, p.FrameID, frameID)
		}
		if p.PartitionKey != subClaims[i].PartitionKey {
			t.Errorf("plans[%d].PartitionKey: %s (want %s)",
				i, p.PartitionKey, subClaims[i].PartitionKey)
		}
		if p.SubClaimHandleID != subClaims[i].ClaimHandleID {
			t.Errorf("plans[%d].SubClaimHandleID mismatch", i)
		}
		if p.Executor != "my-loader" {
			t.Errorf("plans[%d].Executor: %s", i, p.Executor)
		}
	}
}

func TestSplitScopeEmitsNSubClaims_EmptyDescriptorListReturnsEmptyPlans(t *testing.T) {
	t.Parallel()
	parentRun := shared.UUID(uuid.New())
	plans := runtime.PlanFanOutChildren(parentRun, shared.UUID{}, shared.UUID{}, nil, "stub", nil)
	if len(plans) != 0 {
		t.Errorf("empty sub-claims should produce empty plans; got %d", len(plans))
	}
}
