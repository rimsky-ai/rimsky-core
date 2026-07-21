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

func TestFanOutPartitions_IsAPureProjection_NoDedup(t *testing.T) {
	t.Parallel()
	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "dup"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "dup"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3"},
	}
	parts := runtime.FanOutPartitions(subClaims)
	if len(parts) != len(subClaims) {
		t.Fatalf("FanOutPartitions must be a 1:1 projection over its input — sub-claim "+
			"de-duplication, if any, happens upstream at acquisition time, not here; "+
			"got %d outputs for %d inputs", len(parts), len(subClaims))
	}
	for i, p := range parts {
		if p.PartitionKey != subClaims[i].PartitionKey || p.SubClaimHandleID != subClaims[i].ClaimHandleID {
			t.Fatalf("parts[%d] = %+v does not mirror subClaims[%d] = %+v", i, p, i, subClaims[i])
		}
	}
}

func TestChildRunsPerPartitionKey_PreservesProducerOrdering(t *testing.T) {
	t.Parallel()
	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "z-last"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "m-middle"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "a-first"},
	}
	parts := runtime.FanOutPartitions(subClaims)
	if parts[0].PartitionKey != "z-last" {
		t.Errorf("parts[0]: %s (want z-last; ordering must match producer's SubScopes)", parts[0].PartitionKey)
	}
	if parts[1].PartitionKey != "m-middle" {
		t.Errorf("parts[1]: %s", parts[1].PartitionKey)
	}
	if parts[2].PartitionKey != "a-first" {
		t.Errorf("parts[2]: %s", parts[2].PartitionKey)
	}
}
