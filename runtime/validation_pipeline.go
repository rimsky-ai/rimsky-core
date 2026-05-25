// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// validation_pipeline.go — E1 / F9. Template-registration validation
// pipeline.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces / Validation / Pipeline integration.
//
// @concept: validation
//
// Two stages, ordered:
//
//  1. `expected_attributes_schema` static check (existing; lives in
//     `graph/node/template_validator.go::checkAttributesSchema` and
//     runs at canonicalization against the merged effective attribute
//     schema).
//  2. `Validate` RPC fan-out (this file). For each service the template
//     references that advertises Validation for the relevant role,
//     rimsky issues `Validate(...)` and collects errors / warnings.
//
// Unreachable services obey the operator-configured policy
// (`registration.unreachable_validator: strict | permissive_warn`,
// default permissive_warn — registration succeeds with a warning).

package runtime

import (
	"context"
	"encoding/json"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/runtime/clientiface"
)

// Re-exports of the wire-shape types from `runtime/clientiface/`
// (Apache-licensed). The canonical docs live on the alias targets.
// See `data_processing.go` for the licensing-boundary rationale.
type (
	ValidationFinding                = clientiface.ValidationFinding
	ValidationOutcome                = clientiface.ValidationOutcome
	ValidationClient                 = clientiface.ValidationClient
	ValidateExecutorInput            = clientiface.ValidateExecutorInput
	ValidateClaimProducerInput       = clientiface.ValidateClaimProducerInput
	ValidateClaimBinding             = clientiface.ValidateClaimBinding
	ValidateSensorInput              = clientiface.ValidateSensorInput
	ValidateLifecycleSubscriberInput = clientiface.ValidateLifecycleSubscriberInput
	ValidationRegistry               = clientiface.ValidationRegistry
	UnreachableValidatorPolicy       = clientiface.UnreachableValidatorPolicy
)

const (
	// UnreachableValidatorPermissiveWarn is the default — registration
	// succeeds with a warning.
	UnreachableValidatorPermissiveWarn = clientiface.UnreachableValidatorPermissiveWarn
	// UnreachableValidatorStrict — registration fails when any
	// referenced service cannot be reached.
	UnreachableValidatorStrict = clientiface.UnreachableValidatorStrict
)

// RunValidationPipeline iterates the canonicalized template, walks the
// services each node references, and aggregates findings into a single
// `ValidationOutcome`. Unreachable validators obey `policy`.
//
// The pipeline is intentionally additive — it does not re-run the
// static `expected_attributes_schema` checks the template validator
// already performs at canonicalization against the merged effective
// attribute schema. Errors at the static step were already reported
// with a 400; this pipeline runs only after static validation passes.
//
// ExpectedAttributesSchemaLookup returns the named executor's
// advertised `expected_attributes_schema` JSON bytes (the
// ObservabilityCapabilities contribution to the merged effective
// attribute schema). nil → skip executor-side merging (the pipeline
// then sends the bare L2 schema to validators, matching pre-collapse
// behaviour). Empty bytes also mean "no schema visible".
type ExpectedAttributesSchemaLookup func(executor string) ([]byte, bool)

// Empty registry (`reg == nil` or returns nothing) → no-op success.
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

	// Executor-role + ClaimProducer-role checks per node.
	for _, n := range tpl.Nodes {
		if err := runExecutorRoleCheck(ctx, reg, policy, n, tpl, execSchemaLookup, &out); err != nil {
			return out, err
		}
		if err := runClaimProducerRoleChecks(ctx, reg, policy, n, &out); err != nil {
			return out, err
		}
	}

	// Sensor-role validation checks per declared publisher entry.
	// The validation protocol's per-role surface is unchanged; the
	// template-DSL block renames from `sensors:` to `publishers:` but
	// the inner role name on the Validation protocol stays "sensor"
	// (sensors are one kind of publisher; the validation role is
	// kind-shaped).
	for _, publisher := range tpl.Publishers {
		client, ok := reg.Get(publisher.Name)
		if !ok || !clientAdvertisesRole(client, "sensor") {
			continue
		}
		errs, warns, err := client.ValidateSensor(ctx, ValidateSensorInput{
			SensorName:     publisher.Name,
			Kind:           publisher.Kind,
			ResolvedConfig: publisher.Config,
		})
		appendFindings(&out, client.Name(), "sensor", "", errs, warns, err, policy)
	}

	// LifecycleSubscriber-role: no per-template iteration unless
	// the operator's config maps lifecycle subscribers explicitly.
	// V1 ships without that iteration; the per-template lifecycle
	// validation is covered by the `OnTemplateRegistered` fan-out
	// the templates handler already performs.
	_ = templateID

	return out, nil
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
	// Send the merged effective attribute schema (executor's
	// expected_attributes_schema ∪ L1 template defaults ∪ L2 node
	// schema) to executor-side validators. Pre-collapse, the bare L2
	// schema was sufficient because L1 defaults lived in a separate
	// userdata bag; post-collapse, executor validators that read
	// `properties.<key>.default` (e.g. verifier-shape-checks's
	// `checks` field) need to see L1 contributions too. Per spec
	// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
	// §"Design changes" / `concept:validation`.
	var nodeSchema map[string]any
	if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
		nodeSchema = n.Attributes.Schema
	}
	var execSchema map[string]any
	if execSchemaLookup != nil {
		if bytesIn, ok := execSchemaLookup(n.Executor); ok && len(bytesIn) > 0 {
			// Unmarshal failures fall back to a nil executor schema —
			// the static-schema check earlier in registration already
			// reports those, so the pipeline doesn't double-error.
			_ = json.Unmarshal(bytesIn, &execSchema)
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
	aliases := make([]string, 0, len(n.Stores)+len(n.Holds))
	for _, s := range n.Stores {
		aliases = append(aliases, s.AliasOf())
	}
	for alias := range n.Holds {
		aliases = append(aliases, alias)
	}
	errs, warns, err := client.ValidateExecutor(ctx, ValidateExecutorInput{
		NodeAlias:        n.Type,
		AttributesSchema: attrsBytes,
		ClaimAliases:     aliases,
	})
	appendFindings(out, client.Name(), "executor", n.Type, errs, warns, err, policy)
	return nil
}

func runClaimProducerRoleChecks(
	ctx context.Context, reg ValidationRegistry, policy UnreachableValidatorPolicy,
	n spec.TemplateNodeDef, out *ValidationOutcome,
) error {
	if len(n.Stores) == 0 {
		return nil
	}
	// Group per producer name so each producer sees its claims.
	byProducer := map[string][]spec.NodeStoreRef{}
	for _, s := range n.Stores {
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
		errs, warns, err := client.ValidateClaimProducer(ctx, ValidateClaimProducerInput{
			ProducerName: producer,
			Claims:       bindings,
		})
		appendFindings(out, client.Name(), "claim_producer", n.Type, errs, warns, err, policy)
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

// appendFindings folds per-call results into the outcome aggregate.
// RPC errors fold per policy: permissive_warn → warning; strict →
// error.
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

// keep shared imports alive.
var _ = shared.UUID{}
