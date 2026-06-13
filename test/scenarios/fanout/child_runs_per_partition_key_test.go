// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N2 scenario — child_runs_per_partition_key.
//
// The dispatcher inserts one rimsky_node_runs child row per
// SubScopeDescriptor with `child_key = <partition_key>`. The
// (parent_run_id, child_key) pair is the idempotency key; re-creating
// the same child returns the existing row id. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Fan-out template DSL "Mechanics at dispatch" step 4.
//
// This scenario exercises the dispatcher-side projection's
// per-child-key uniqueness contract by tracking the partition keys
// `FanOutPartitions` produces and asserting that each partition
// carries a distinct child_key matching a sub-claim.
package fanout

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestChildRunsPerPartitionKey_OneChildPerKey(t *testing.T) {
	t.Parallel()
	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p1", Address: json.RawMessage(`{}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p2", Address: json.RawMessage(`{}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3", Address: json.RawMessage(`{}`)},
	}
	parts := runtime.FanOutPartitions(subClaims)
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		if seen[p.PartitionKey] {
			t.Errorf("duplicate partition_key in partitions: %s", p.PartitionKey)
		}
		seen[p.PartitionKey] = true
	}
	if len(seen) != len(subClaims) {
		t.Errorf("expected %d distinct partition_keys, got %d", len(subClaims), len(seen))
	}
}

// PartitionKey ordering matches the producer's SubScope ordering — the
// caller may sort to make assertions reproducible.
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
