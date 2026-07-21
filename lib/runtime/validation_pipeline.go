// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: validation

package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type (
	ValidationFinding                = clientiface.ValidationFinding
	ValidationOutcome                = clientiface.ValidationOutcome
	ValidationClient                 = clientiface.ValidationClient
	ValidateExecutorInput            = clientiface.ValidateExecutorInput
	ValidateClaimProducerInput       = clientiface.ValidateClaimProducerInput
	ValidateClaimBinding             = clientiface.ValidateClaimBinding
	ValidatePublisherInput           = clientiface.ValidatePublisherInput
	ValidateLifecycleSubscriberInput = clientiface.ValidateLifecycleSubscriberInput
	ValidationRegistry               = clientiface.ValidationRegistry
	UnreachableValidatorPolicy       = clientiface.UnreachableValidatorPolicy
)

const UnreachableValidatorPermissiveWarn = clientiface.UnreachableValidatorPermissiveWarn

const UnreachableValidatorStrict = clientiface.UnreachableValidatorStrict

type ExpectedAttributesSchemaLookup func(executor string) ([]byte, bool)

func RunValidationPipeline(
	ctx context.Context,
	reg ValidationRegistry,
	tpl spec.TemplateSpec,
	templateID string,
	policy UnreachableValidatorPolicy,
	execSchemaLookup ExpectedAttributesSchemaLookup,
) (ValidationOutcome, error) {
	out := ValidationOutcome{}
	if reg == nil {
		return out, nil
	}
	if policy == "" {
		policy = UnreachableValidatorPermissiveWarn
	}

	for _, n := range tpl.Nodes {
		if err := runExecutorRoleCheck(ctx, reg, policy, n, tpl, execSchemaLookup, &out); err != nil {
			return out, err
		}
		if err := runClaimProducerRoleChecks(ctx, reg, policy, n, &out); err != nil {
			return out, err
		}
	}

	for _, publisher := range tpl.Publishers {
		client, ok := reg.Get(publisher.Name)
		if !ok || !clientAdvertisesRole(client, "publisher") {
			continue
		}
		res, err := client.ValidatePublisher(ctx, ValidatePublisherInput{
			PublisherName:  publisher.Name,
			Kind:           publisher.Kind,
			ResolvedConfig: publisher.Config,
		})
		appendFindings(&out, client.Name(), "publisher", "", res.Errors, res.Warnings, err, policy)
	}

	runLifecycleSubscriberRoleChecks(ctx, reg, policy, templateID, &out)

	return out, nil
}

func runLifecycleSubscriberRoleChecks(
	ctx context.Context, reg ValidationRegistry, policy UnreachableValidatorPolicy,
	templateID string, out *ValidationOutcome,
) {
	for _, client := range reg.All() {
		if !clientAdvertisesRole(client, "lifecycle_subscriber") {
			continue
		}
		res, err := client.ValidateLifecycleSubscriber(ctx, ValidateLifecycleSubscriberInput{
			SubscriberName: client.Name(),
			TemplateID:     templateID,
		})
		appendFindings(out, client.Name(), "lifecycle_subscriber", "", res.Errors, res.Warnings, err, policy)
	}
}

func runExecutorRoleCheck(
	ctx context.Context, reg ValidationRegistry, policy UnreachableValidatorPolicy,
	n spec.TemplateNodeDef, tpl spec.TemplateSpec,
	execSchemaLookup ExpectedAttributesSchemaLookup,
	out *ValidationOutcome,
) error {
	if n.Executor == "" {
		return nil
	}
	client, ok := reg.Get(n.Executor)
	if !ok || !clientAdvertisesRole(client, "executor") {
		return nil
	}
	// @concept: validation
	var nodeSchema map[string]any
	if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
		nodeSchema = n.Attributes.Schema
	}
	var execSchema map[string]any
	if execSchemaLookup != nil {
		if bytesIn, ok := execSchemaLookup(n.Executor); ok && len(bytesIn) > 0 {
			if err := json.Unmarshal(bytesIn, &execSchema); err != nil {
				out.Warnings = append(out.Warnings, ValidationFinding{
					ServiceName: client.Name(),
					Role:        "executor",
					NodeAlias:   n.Type,
					Class:       "expected_attributes_schema_malformed",
					Message:     fmt.Sprintf("advertised expected-attributes schema is not valid JSON: %s", err.Error()),
				})
				execSchema = nil
			}
		}
	}
	var l1Defaults map[string]any
	if tpl.Defaults != nil && tpl.Defaults.Attributes != nil {
		l1Defaults = tpl.Defaults.Attributes.ByExecutor[n.Executor]
	}
	var attrsBytes []byte
	merged := node.MergeAttributeDefaults(execSchema, l1Defaults, nodeSchema)
	if merged != nil {
		b, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		attrsBytes = b
	}
	aliases := make([]string, 0, len(n.ClaimProducers)+len(n.Holds))
	for _, s := range n.ClaimProducers {
		aliases = append(aliases, s.AliasOf())
	}
	for alias, binding := range n.Holds {
		aliases = append(aliases, node.EffectiveHoldsLocalAlias(alias, binding))
	}
	res, err := client.ValidateExecutor(ctx, ValidateExecutorInput{
		NodeAlias:        n.Type,
		AttributesSchema: attrsBytes,
		ClaimAliases:     aliases,
	})
	appendFindings(out, client.Name(), "executor", n.Type, res.Errors, res.Warnings, err, policy)
	return nil
}

func runClaimProducerRoleChecks(
	ctx context.Context, reg ValidationRegistry, policy UnreachableValidatorPolicy,
	n spec.TemplateNodeDef, out *ValidationOutcome,
) error {
	if len(n.ClaimProducers) == 0 {
		return nil
	}
	byProducer := map[string][]spec.NodeClaimProducerRef{}
	for _, s := range n.ClaimProducers {
		byProducer[s.Name] = append(byProducer[s.Name], s)
	}
	for producer, refs := range byProducer {
		client, ok := reg.Get(producer)
		if !ok || !clientAdvertisesRole(client, "claim_producer") {
			continue
		}
		bindings := make([]ValidateClaimBinding, 0, len(refs))
		for _, s := range refs {
			bindings = append(bindings, ValidateClaimBinding{
				NodeAlias:  n.Type,
				ClaimAlias: s.AliasOf(),
				Selector:   s.Selector,
				Intent:     s.Intent,
				Lifetime:   s.Lifetime,
				Data:       s.Data,
			})
		}
		res, err := client.ValidateClaimProducer(ctx, ValidateClaimProducerInput{
			ProducerName: producer,
			Claims:       bindings,
		})
		appendFindings(out, client.Name(), "claim_producer", n.Type, res.Errors, res.Warnings, err, policy)
	}
	return nil
}

func clientAdvertisesRole(c ValidationClient, role string) bool {
	for _, r := range c.SupportedRoles() {
		if r == role {
			return true
		}
	}
	return false
}

func appendFindings(
	out *ValidationOutcome,
	service, role, nodeAlias string,
	errs, warns []ValidationFinding,
	rpcErr error,
	policy UnreachableValidatorPolicy,
) {
	if rpcErr != nil {
		finding := ValidationFinding{
			ServiceName: service,
			Role:        role,
			NodeAlias:   nodeAlias,
			Class:       "validator_unreachable",
			Message:     rpcErr.Error(),
		}
		if policy == UnreachableValidatorStrict {
			out.Errors = append(out.Errors, finding)
		} else {
			out.Warnings = append(out.Warnings, finding)
		}
		return
	}
	for _, e := range errs {
		e.ServiceName = service
		e.Role = role
		if e.NodeAlias == "" {
			e.NodeAlias = nodeAlias
		}
		out.Errors = append(out.Errors, e)
	}
	for _, w := range warns {
		w.ServiceName = service
		w.Role = role
		if w.NodeAlias == "" {
			w.NodeAlias = nodeAlias
		}
		out.Warnings = append(out.Warnings, w)
	}
}

var _ = shared.UUID{}
