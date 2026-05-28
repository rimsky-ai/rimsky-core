// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// validation.go — rimsky-side wire-shape types for the Validation
// protocol. See package doc in `data_processing.go` for the
// licensing-boundary rationale.

package clientiface

import "context"

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
// `runtime/peer/validation_client.go` (the wired gRPC client) or in
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
