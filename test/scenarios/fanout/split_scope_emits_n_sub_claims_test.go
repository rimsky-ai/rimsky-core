// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package fanout

import (
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

	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-east-1"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-west-2"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "eu-west-1"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "ap-south-1"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "sa-east-1"},
	}
	in := runtime.ChildExecutionInput{
		ParentRunID: parentRun,
		FrameID:     frameID,
		Partitions:  runtime.FanOutPartitions(subClaims),
		Children: []runtime.ChildRunSpec{{
			NodeID:                 parentNode,
			Executor:               "my-loader",
			RequiredClaimProducers: []string{"parquet-store"},
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
