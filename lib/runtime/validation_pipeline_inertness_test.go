// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
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
func (v *capturingValidator) ValidateExecutor(context.Context, ValidateExecutorInput) ([]ValidationFinding, []ValidationFinding, error) {
	return nil, nil, nil
}

func (v *capturingValidator) ValidateClaimProducer(
	_ context.Context, in ValidateClaimProducerInput,
) ([]ValidationFinding, []ValidationFinding, error) {
	v.claimProducerIn = in
	v.sawClaimProducer = true
	return nil, nil, nil
}

func (v *capturingValidator) ValidatePublisher(
	_ context.Context, in ValidatePublisherInput,
) ([]ValidationFinding, []ValidationFinding, error) {
	v.publisherIn = in
	v.sawPublisher = true
	return nil, nil, nil
}

func (v *capturingValidator) ValidateLifecycleSubscriber(context.Context, ValidateLifecycleSubscriberInput) ([]ValidationFinding, []ValidationFinding, error) {
	return nil, nil, nil
}

type singleValidatorRegistry struct {
	byName map[string]ValidationClient
}

func (r *singleValidatorRegistry) Get(name string) (ValidationClient, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// @concept: inertness
func TestRunValidationPipeline_ForwardsClaimAndPublisherBytesVerbatim(t *testing.T) {
	v := &capturingValidator{name: "producer-a"}
	pubV := &capturingValidator{name: "publisher-a"}
	reg := &singleValidatorRegistry{byName: map[string]ValidationClient{
		"producer-a":  v,
		"publisher-a": pubV,
	}}

	claimData := json.RawMessage(`{  "key"  :  "value"  ,  "n": 1  }`)
	pubConfig := json.RawMessage(`{  "topic"  :  "orders"  ,  "n": 2  }`)

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
