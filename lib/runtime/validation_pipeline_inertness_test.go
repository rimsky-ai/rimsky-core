// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type capturingValidator struct {
	name             string
	claimProducerIn  ValidateClaimProducerInput
	publisherIn      ValidatePublisherInput
	sawClaimProducer bool
	sawPublisher     bool
}

func (v *capturingValidator) Name() string { return v.name }
func (v *capturingValidator) SupportedRoles() []string {
	return []string{"claim_producer", "publisher"}
}
func (v *capturingValidator) ValidateExecutor(context.Context, ValidateExecutorInput) (ValidationOutcome, error) {
	return ValidationOutcome{}, nil
}

func (v *capturingValidator) ValidateClaimProducer(
	_ context.Context, in ValidateClaimProducerInput,
) (ValidationOutcome, error) {
	v.claimProducerIn = in
	v.sawClaimProducer = true
	return ValidationOutcome{}, nil
}

func (v *capturingValidator) ValidatePublisher(
	_ context.Context, in ValidatePublisherInput,
) (ValidationOutcome, error) {
	v.publisherIn = in
	v.sawPublisher = true
	return ValidationOutcome{}, nil
}

func (v *capturingValidator) ValidateLifecycleSubscriber(context.Context, ValidateLifecycleSubscriberInput) (ValidationOutcome, error) {
	return ValidationOutcome{}, nil
}

type singleValidatorRegistry struct {
	byName map[string]ValidationClient
}

func (r *singleValidatorRegistry) Get(name string) (ValidationClient, bool) {
	c, ok := r.byName[name]
	return c, ok
}

func (r *singleValidatorRegistry) All() []ValidationClient {
	out := make([]ValidationClient, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}

// @concept: inertness
func TestRunValidationPipeline_ForwardsClaimAndPublisherBytesVerbatim(t *testing.T) {
	v := &capturingValidator{name: "producer-a"}
	pubV := &capturingValidator{name: "publisher-a"}
	reg := &singleValidatorRegistry{byName: map[string]ValidationClient{
		"producer-a":  v,
		"publisher-a": pubV,
	}}

	claimData := spec.RawJSON(`{  "key"  :  "value"  ,  "n": 1  }`)
	pubConfig := spec.RawJSON(`{  "topic"  :  "orders"  ,  "n": 2  }`)

	tpl := spec.TemplateSpec{
		Nodes: []spec.TemplateNodeDef{
			{
				Type: "worker",
				ClaimProducers: []spec.NodeClaimProducerRef{
					{Name: "producer-a", Selector: "sel", Intent: "rw", Alias: "data", Data: claimData},
				},
			},
		},
		Publishers: []spec.PublisherSpec{
			{Name: "publisher-a", Kind: "http", Config: pubConfig},
		},
	}

	out, err := RunValidationPipeline(context.Background(), reg, tpl, "tpl-1", UnreachableValidatorPermissiveWarn, nil)
	if err != nil {
		t.Fatalf("RunValidationPipeline: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", out.Errors)
	}

	if !v.sawClaimProducer {
		t.Fatal("ValidateClaimProducer was never invoked")
	}
	if len(v.claimProducerIn.Claims) != 1 {
		t.Fatalf("ValidateClaimProducer.Claims: got %d entries, want 1", len(v.claimProducerIn.Claims))
	}
	if string(v.claimProducerIn.Claims[0].Data) != string(claimData) {
		t.Fatalf("ValidateClaimProducer.Claims[0].Data must be forwarded byte-verbatim "+
			"(no re-marshal/normalization): got %q, want %q",
			v.claimProducerIn.Claims[0].Data, claimData)
	}

	if !pubV.sawPublisher {
		t.Fatal("ValidatePublisher was never invoked")
	}
	if string(pubV.publisherIn.ResolvedConfig) != string(pubConfig) {
		t.Fatalf("ValidatePublisher.ResolvedConfig must be forwarded byte-verbatim "+
			"(no re-marshal/normalization): got %q, want %q",
			pubV.publisherIn.ResolvedConfig, pubConfig)
	}
}

type executorCapturingValidator struct {
	capturingValidator
	executorIn ValidateExecutorInput
	sawExec    bool
}

func (v *executorCapturingValidator) SupportedRoles() []string { return []string{"executor"} }

func (v *executorCapturingValidator) ValidateExecutor(
	_ context.Context, in ValidateExecutorInput,
) (ValidationOutcome, error) {
	v.executorIn = in
	v.sawExec = true
	return ValidationOutcome{}, nil
}

// @concept: validation
func TestRunValidationPipeline_MalformedExecutorSchemaWarnsAndDropsExecutorLayer(t *testing.T) {
	v := &executorCapturingValidator{capturingValidator: capturingValidator{name: "exec-a"}}
	reg := &singleValidatorRegistry{byName: map[string]ValidationClient{"exec-a": v}}

	tpl := spec.TemplateSpec{
		Nodes: []spec.TemplateNodeDef{
			{Type: "worker", Executor: "exec-a"},
		},
	}

	lookup := func(executor string) ([]byte, bool) {
		return []byte(`{not-json`), true
	}

	out, err := RunValidationPipeline(context.Background(), reg, tpl, "tpl-1", UnreachableValidatorPermissiveWarn, lookup)
	if err != nil {
		t.Fatalf("RunValidationPipeline: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("a malformed advertised schema must degrade to a warning, not a hard error: %v", out.Errors)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("expected exactly one warning for the malformed schema, got %d: %v", len(out.Warnings), out.Warnings)
	}
	if out.Warnings[0].Class != "expected_attributes_schema_malformed" {
		t.Fatalf("Warnings[0].Class = %q; want expected_attributes_schema_malformed", out.Warnings[0].Class)
	}
	if !v.sawExec {
		t.Fatal("ValidateExecutor was never invoked despite the malformed schema; the executor layer should " +
			"drop out of the merged schema, not abort the whole node's validation")
	}
}
