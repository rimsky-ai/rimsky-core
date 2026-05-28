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
// This scenario exercises the dispatcher-side plan loop's
// per-child-key uniqueness contract by tracking the partition keys
// the projection produces and asserting that each plan carries a
// distinct child_key matching a sub-claim.
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
	parentRun := shared.UUID(uuid.New())
	subClaims := []runtime.SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p1", Address: json.RawMessage(`{}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p2", Address: json.RawMessage(`{}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3", Address: json.RawMessage(`{}`)},
	}
	plans := runtime.PlanFanOutChildren(parentRun, shared.UUID{}, shared.UUID{},
		subClaims, "stub", nil)
	seen := make(map[string]bool, len(plans))
	for _, p := range plans {
		if seen[p.PartitionKey] {
			t.Errorf("duplicate partition_key in plans: %s", p.PartitionKey)
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
	plans := runtime.PlanFanOutChildren(shared.UUID(uuid.New()), shared.UUID{}, shared.UUID{},
		subClaims, "stub", nil)
	if plans[0].PartitionKey != "z-last" {
		t.Errorf("plans[0]: %s (want z-last; ordering must match producer's SubScopes)", plans[0].PartitionKey)
	}
	if plans[1].PartitionKey != "m-middle" {
		t.Errorf("plans[1]: %s", plans[1].PartitionKey)
	}
	if plans[2].PartitionKey != "a-first" {
		t.Errorf("plans[2]: %s", plans[2].PartitionKey)
	}
}
