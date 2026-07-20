// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: claim-scope
func TestBuildLockSpecs_PopulatesTemplateAndInstanceScopeFromInstance(t *testing.T) {
	ctx := context.Background()
	instID := shared.UUID(uuid.New())
	inst := &persistence.InstanceRow{
		ID:           instID,
		TemplateHash: "sha256-scope-test",
	}
	def := &node.TemplateNodeDef{
		Type: "acquirer",
		ClaimProducers: []node.NodeClaimProducerRef{
			{Name: "some-store", Selector: "/x", Intent: "rw", Alias: "data"},
		},
	}

	specs, _, err := buildLockSpecs(ctx, RunArgs{}, nil, nil, def, nil, inst, shared.UUID{}, shared.UUID{}, shared.UUID{})
	if err != nil {
		t.Fatalf("buildLockSpecs: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("buildLockSpecs returned %d specs, want 1", len(specs))
	}
	cs, ok := specs[0].(claimproducer.ClaimSpec)
	if !ok {
		t.Fatalf("spec[0] type = %T, want claimproducer.ClaimSpec", specs[0])
	}
	if cs.TemplateID == "" || cs.TemplateID != inst.TemplateHash {
		t.Fatalf("ClaimSpec.TemplateID = %q, want non-empty %q "+
			"(runner_locks.go must populate the OpenRequest scope envelope's template_id from the instance)",
			cs.TemplateID, inst.TemplateHash)
	}
	if cs.InstanceID == "" || cs.InstanceID != inst.ID.String() {
		t.Fatalf("ClaimSpec.InstanceID = %q, want non-empty %q "+
			"(runner_locks.go must populate the OpenRequest scope envelope's instance_id from the instance)",
			cs.InstanceID, inst.ID.String())
	}
}
