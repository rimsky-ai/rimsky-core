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
// This scenario validates the `FanOutPartitions` projection plus the
// fan-out shape of the unified-dispatch input — the dispatcher builds
// one `ChildExecutionInput` whose partitions map one-to-one onto the
// producer's sub-claims and whose single child-run spec carries the
// leaf executor + required stores. The unit-level coverage in
// runtime/fanout_dispatch_test.go pins the projection rules; this
// scenario exercises the additional contract that the per-partition
// descriptor carries the sub-claim handle id + partition_key
// one-to-one and the input addresses the parent run / node / frame.
package fanout

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestSplitScopeEmitsNSubClaims_InputProjectsOnePartitionPerSubScope(t *testing.T) {
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
	// Build the input the same way the fan-out dispatch wrapper does:
	// N partitions from the sub-claims, one child-run spec re-using the
	// parent's node + the leaf executor + required stores.
	in := runtime.ChildExecutionInput{
		ParentRunID: parentRun,
		FrameID:     frameID,
		Partitions:  runtime.FanOutPartitions(subClaims),
		Children: []runtime.ChildRunSpec{{
			NodeID:         parentNode,
			Executor:       "my-loader",
			RequiredStores: []string{"parquet-store"},
		}},
	}
	if len(in.Partitions) != len(subClaims) {
		t.Fatalf("partitions: %d (want %d, one per sub-claim)", len(in.Partitions), len(subClaims))
	}
	if in.ParentRunID != parentRun {
		t.Errorf("in.ParentRunID: %s (want %s)", in.ParentRunID, parentRun)
	}
	if in.FrameID != frameID {
		t.Errorf("in.FrameID: %s (want %s)", in.FrameID, frameID)
	}
	if len(in.Children) != 1 {
		t.Fatalf("fan-out input must carry exactly one child-run spec; got %d", len(in.Children))
	}
	if in.Children[0].NodeID != parentNode {
		t.Errorf("child spec NodeID: %s (want %s)", in.Children[0].NodeID, parentNode)
	}
	if in.Children[0].Executor != "my-loader" {
		t.Errorf("child spec Executor: %s", in.Children[0].Executor)
	}
	for i, p := range in.Partitions {
		if p.PartitionKey != subClaims[i].PartitionKey {
			t.Errorf("partitions[%d].PartitionKey: %s (want %s)",
				i, p.PartitionKey, subClaims[i].PartitionKey)
		}
		if p.SubClaimHandleID != subClaims[i].ClaimHandleID {
			t.Errorf("partitions[%d].SubClaimHandleID mismatch", i)
		}
	}
}

func TestSplitScopeEmitsNSubClaims_EmptyDescriptorListReturnsEmptyPartitions(t *testing.T) {
	t.Parallel()
	parts := runtime.FanOutPartitions(nil)
	if len(parts) != 0 {
		t.Errorf("empty sub-claims should produce empty partitions; got %d", len(parts))
	}
}
