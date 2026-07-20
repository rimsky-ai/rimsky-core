// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package clientiface

import "context"

type ValidationFinding struct {
	ServiceName string `json:"service_name"`
	Role        string `json:"role"`
	NodeAlias   string `json:"node_alias,omitempty"`
	Class       string `json:"class,omitempty"`
	Message     string `json:"message,omitempty"`
	Path        string `json:"path,omitempty"`
}

type ValidationOutcome struct {
	Errors   []ValidationFinding
	Warnings []ValidationFinding
}

type ValidationClient interface {
	Name() string

	SupportedRoles() []string

	ValidateExecutor(ctx context.Context, in ValidateExecutorInput) ([]ValidationFinding, []ValidationFinding, error)

	ValidateClaimProducer(ctx context.Context, in ValidateClaimProducerInput) ([]ValidationFinding, []ValidationFinding, error)

	ValidatePublisher(ctx context.Context, in ValidatePublisherInput) ([]ValidationFinding, []ValidationFinding, error)

	ValidateLifecycleSubscriber(ctx context.Context, in ValidateLifecycleSubscriberInput) ([]ValidationFinding, []ValidationFinding, error)
}

type ValidateExecutorInput struct {
	NodeAlias        string
	AttributesSchema []byte
	ClaimAliases     []string
}

type ValidateClaimProducerInput struct {
	ProducerName string
	Claims       []ValidateClaimBinding
}

type ValidateClaimBinding struct {
	NodeAlias        string
	ClaimAlias       string
	Selector         string
	Intent           string
	Lifetime         string
	Data             []byte
	PartitionRequest []byte
}

type ValidatePublisherInput struct {
	PublisherName  string
	Kind           string
	ResolvedConfig []byte
}

type ValidateLifecycleSubscriberInput struct {
	SubscriberName string
	TemplateID     string
}

type ValidationRegistry interface {
	Get(name string) (ValidationClient, bool)
	All() []ValidationClient
}

type UnreachableValidatorPolicy string

const (
	UnreachableValidatorPermissiveWarn UnreachableValidatorPolicy = "permissive_warn"
	UnreachableValidatorStrict         UnreachableValidatorPolicy = "strict"
)
