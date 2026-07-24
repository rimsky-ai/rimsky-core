// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
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

	specs, _, err := buildLockSpecs(ctx, RunArgs{}, nil, def, nil, inst, shared.UUID{}, shared.UUID{}, shared.UUID{}, nil)
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

// @decision: substitution-failure-routes-with-substitution
func TestBuildLockSpecs_LockNameSubstitutionFailureCarriesLockNameSite(t *testing.T) {
	ctx := context.Background()
	def := &node.TemplateNodeDef{
		Type:  "acquirer",
		Locks: []node.NodeLockRef{{Name: "{{deps.missing.field}}"}},
	}

	_, _, err := buildLockSpecs(ctx, RunArgs{}, nil, def, nil, nil, shared.UUID{}, shared.UUID{}, shared.UUID{}, nil)
	var subErr *lockSpecSubstitutionError
	if !errors.As(err, &subErr) {
		t.Fatalf("expected *lockSpecSubstitutionError; got %T: %v", err, err)
	}
	if subErr.Site != substitutionSiteLockName {
		t.Fatalf("Site = %q, want %q", subErr.Site, substitutionSiteLockName)
	}
	if subErr.Directive != "{{deps.missing.field}}" {
		t.Fatalf("Directive = %q, want the raw lock-name directive", subErr.Directive)
	}
}

// @decision: substitution-failure-routes-with-substitution
func TestBuildLockSpecs_SelectorSubstitutionFailureCarriesScopeSite(t *testing.T) {
	ctx := context.Background()
	def := &node.TemplateNodeDef{
		Type: "acquirer",
		ClaimProducers: []node.NodeClaimProducerRef{
			{Name: "some-store", Selector: "/x/{{deps.missing.field}}", Intent: "rw"},
		},
	}

	_, _, err := buildLockSpecs(ctx, RunArgs{}, nil, def, nil, nil, shared.UUID{}, shared.UUID{}, shared.UUID{}, nil)
	var subErr *lockSpecSubstitutionError
	if !errors.As(err, &subErr) {
		t.Fatalf("expected *lockSpecSubstitutionError; got %T: %v", err, err)
	}
	if subErr.Site != substitutionSiteScope {
		t.Fatalf("Site = %q, want %q", subErr.Site, substitutionSiteScope)
	}
	if subErr.Directive != "/x/{{deps.missing.field}}" {
		t.Fatalf("Directive = %q, want the raw selector directive", subErr.Directive)
	}
}
