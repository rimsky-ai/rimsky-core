// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type executorRoleCapturingValidator struct {
	name string
	in   ValidateExecutorInput
	saw  bool
}

func (v *executorRoleCapturingValidator) Name() string             { return v.name }
func (v *executorRoleCapturingValidator) SupportedRoles() []string { return []string{"executor"} }

func (v *executorRoleCapturingValidator) ValidateExecutor(_ context.Context, in ValidateExecutorInput) (ValidationOutcome, error) {
	v.in = in
	v.saw = true
	return ValidationOutcome{}, nil
}

func (v *executorRoleCapturingValidator) ValidateClaimProducer(context.Context, ValidateClaimProducerInput) (ValidationOutcome, error) {
	return ValidationOutcome{}, nil
}

func (v *executorRoleCapturingValidator) ValidatePublisher(context.Context, ValidatePublisherInput) (ValidationOutcome, error) {
	return ValidationOutcome{}, nil
}

func (v *executorRoleCapturingValidator) ValidateLifecycleSubscriber(context.Context, ValidateLifecycleSubscriberInput) (ValidationOutcome, error) {
	return ValidationOutcome{}, nil
}

// @concept: claim-co-holdership
func TestRunExecutorRoleCheck_PassesRenamedAliasNotMapKeyForHoldsAs(t *testing.T) {
	v := &executorRoleCapturingValidator{name: "worker-executor"}
	reg := &singleValidatorRegistry{byName: map[string]ValidationClient{
		"worker-executor": v,
	}}

	tpl := spec.TemplateSpec{
		Nodes: []spec.TemplateNodeDef{
			{
				Type:     "worker",
				Executor: "worker-executor",
				Holds: map[string]spec.HoldsBinding{
					"map-key-alias": {From: "upstream", As: "renamed-alias"},
				},
			},
		},
	}

	out, err := RunValidationPipeline(context.Background(), reg, tpl, "tpl-1", UnreachableValidatorPermissiveWarn, nil)
	if err != nil {
		t.Fatalf("RunValidationPipeline: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", out.Errors)
	}
	if !v.saw {
		t.Fatal("ValidateExecutor was never invoked")
	}
	found := false
	for _, a := range v.in.ClaimAliases {
		if a == "map-key-alias" {
			t.Fatalf("ClaimAliases contains the un-renamed map key %q; runner_locks.go resolves the claim under "+
				"the renamed alias %q at dispatch time, so validating against the map key makes any template "+
				"referencing the renamed alias fail validation while it works at runtime", a, "renamed-alias")
		}
		if a == "renamed-alias" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ClaimAliases = %v, want it to contain the renamed alias %q", v.in.ClaimAliases, "renamed-alias")
	}
}
