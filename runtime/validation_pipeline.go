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
//  1. `userdata_schema` static check (existing; lives in
//     `graph/node/template_validator.go` and runs at canonicalization).
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

	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// ValidationFinding mirrors the proto ValidationFinding without
// importing the proto-gen package here (the controlapi layer owns the
// HTTP projection; this struct stays minimal so the runtime
// dependency graph stays narrow).
type ValidationFinding struct {
	ServiceName string `json:"service_name"`
	Role        string `json:"role"`
	NodeAlias   string `json:"node_alias,omitempty"`
	Class       string `json:"class,omitempty"`
	Message     string `json:"message,omitempty"`
	Path        string `json:"path,omitempty"`
}

// ValidationOutcome is the consolidated return shape from the
// pipeline. Empty Errors + empty Warnings == clean pass.
type ValidationOutcome struct {
	Errors   []ValidationFinding
	Warnings []ValidationFinding
}

// ValidationClient is the rimsky-side wrapper around the per-service
// Validation gRPC client. Implementations live in
// `runtime/remote/validation_client.go` (the wired gRPC client) or in
// test fixtures.
type ValidationClient interface {
	// Name returns the operator-configured peer name.
	Name() string

	// SupportedRoles is the advertised `validation_supported_roles`
	// from the service's Capabilities handshake. Empty when the peer
	// does not advertise the Validation mix-in.
	SupportedRoles() []string

	// ValidateExecutor runs the executor-role check.
	ValidateExecutor(ctx context.Context, in ValidateExecutorInput) ([]ValidationFinding, []ValidationFinding, error)

	// ValidateClaimProducer runs the claim_producer-role check.
	ValidateClaimProducer(ctx context.Context, in ValidateClaimProducerInput) ([]ValidationFinding, []ValidationFinding, error)

	// ValidateSensor runs the sensor-role check.
	ValidateSensor(ctx context.Context, in ValidateSensorInput) ([]ValidationFinding, []ValidationFinding, error)

	// ValidateLifecycleSubscriber runs the lifecycle_subscriber-role check.
	ValidateLifecycleSubscriber(ctx context.Context, in ValidateLifecycleSubscriberInput) ([]ValidationFinding, []ValidationFinding, error)
}

// ValidateExecutorInput is the per-call payload for executor-role
// validation. Mirrors the proto ExecutorContext.
type ValidateExecutorInput struct {
	NodeAlias        string
	Userdata         []byte
	AttributesSchema []byte
	ClaimAliases     []string
}

// ValidateClaimProducerInput is the per-call payload for
// claim_producer-role validation.
type ValidateClaimProducerInput struct {
	ProducerName string
	Claims       []ValidateClaimBinding
}

// ValidateClaimBinding mirrors the proto ClaimBinding.
type ValidateClaimBinding struct {
	NodeAlias        string
	ClaimAlias       string
	Selector         string
	Intent           string
	Lifetime         string
	Data             []byte
	PartitionRequest []byte
}

// ValidateSensorInput is the per-call payload for sensor-role validation.
type ValidateSensorInput struct {
	SensorName     string
	Kind           string
	ResolvedConfig []byte
}

// ValidateLifecycleSubscriberInput is the per-call payload for
// lifecycle-subscriber-role validation.
type ValidateLifecycleSubscriberInput struct {
	SubscriberName string
	TemplateID     string
}

// ValidationRegistry resolves a service name to its ValidationClient.
// Returns ok=false when the named service does not advertise the
// Validation mix-in on this process.
type ValidationRegistry interface {
	Get(name string) (ValidationClient, bool)
}

// UnreachableValidatorPolicy controls how the pipeline reacts to a
// per-service Validate RPC error (unreachable, deadline, etc).
type UnreachableValidatorPolicy string

const (
	// UnreachableValidatorPermissiveWarn is the default — registration
	// succeeds with a warning.
	UnreachableValidatorPermissiveWarn UnreachableValidatorPolicy = "permissive_warn"
	// UnreachableValidatorStrict — registration fails when any
	// referenced service cannot be reached.
	UnreachableValidatorStrict UnreachableValidatorPolicy = "strict"
)

// RunValidationPipeline iterates the canonicalized template, walks the
// services each node references, and aggregates findings into a single
// `ValidationOutcome`. Unreachable validators obey `policy`.
//
// The pipeline is intentionally additive — it does not re-run the
// static `userdata_schema` checks the template validator already
// performs at canonicalization. Errors at the static step were
// already reported with a 400; this pipeline runs only after static
// validation passes.
//
// Empty registry (`reg == nil` or returns nothing) → no-op success.
func RunValidationPipeline(
	ctx context.Context,
	reg ValidationRegistry,
	tpl spec.TemplateSpec,
	templateID string,
	policy UnreachableValidatorPolicy,
) (ValidationOutcome, error) {
	out := ValidationOutcome{}
	if reg == nil {
		return out, nil
	}
	if policy == "" {
		policy = UnreachableValidatorPermissiveWarn
	}

	// Executor-role + ClaimProducer-role checks per node.
	for _, node := range tpl.Nodes {
		if err := runExecutorRoleCheck(ctx, reg, policy, node, &out); err != nil {
			return out, err
		}
		if err := runClaimProducerRoleChecks(ctx, reg, policy, node, &out); err != nil {
			return out, err
		}
	}

	// Sensor-role checks per declared sensor.
	for _, sensor := range tpl.Sensors {
		client, ok := reg.Get(sensor.Name)
		if !ok || !clientAdvertisesRole(client, "sensor") {
			continue
		}
		errs, warns, err := client.ValidateSensor(ctx, ValidateSensorInput{
			SensorName:     sensor.Name,
			Kind:           sensor.Kind,
			ResolvedConfig: sensor.Config,
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
	node spec.TemplateNodeDef, out *ValidationOutcome,
) error {
	if node.Executor == "" {
		return nil
	}
	client, ok := reg.Get(node.Executor)
	if !ok || !clientAdvertisesRole(client, "executor") {
		return nil
	}
	var userdataBytes []byte
	if len(node.Userdata) > 0 {
		b, err := json.Marshal(node.Userdata)
		if err != nil {
			return err
		}
		userdataBytes = b
	}
	var attrsBytes []byte
	if node.Attributes != nil && len(node.Attributes.Schema) > 0 {
		b, err := json.Marshal(node.Attributes.Schema)
		if err != nil {
			return err
		}
		attrsBytes = b
	}
	aliases := make([]string, 0, len(node.Stores)+len(node.Holds))
	for _, s := range node.Stores {
		aliases = append(aliases, s.AliasOf())
	}
	for alias := range node.Holds {
		aliases = append(aliases, alias)
	}
	errs, warns, err := client.ValidateExecutor(ctx, ValidateExecutorInput{
		NodeAlias:        node.Type,
		Userdata:         userdataBytes,
		AttributesSchema: attrsBytes,
		ClaimAliases:     aliases,
	})
	appendFindings(out, client.Name(), "executor", node.Type, errs, warns, err, policy)
	return nil
}

func runClaimProducerRoleChecks(
	ctx context.Context, reg ValidationRegistry, policy UnreachableValidatorPolicy,
	node spec.TemplateNodeDef, out *ValidationOutcome,
) error {
	if len(node.Stores) == 0 {
		return nil
	}
	// Group per producer name so each producer sees its claims.
	byProducer := map[string][]spec.NodeStoreRef{}
	for _, s := range node.Stores {
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
				NodeAlias:  node.Type,
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
		appendFindings(out, client.Name(), "claim_producer", node.Type, errs, warns, err, policy)
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
