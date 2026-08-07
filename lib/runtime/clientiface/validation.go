// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package clientiface

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

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

	ValidateExecutor(ctx context.Context, in ValidateExecutorInput) (ValidationOutcome, error)

	ValidateClaimProducer(ctx context.Context, in ValidateClaimProducerInput) (ValidationOutcome, error)

	ValidatePublisher(ctx context.Context, in ValidatePublisherInput) (ValidationOutcome, error)

	ValidateLifecycleSubscriber(ctx context.Context, in ValidateLifecycleSubscriberInput) (ValidationOutcome, error)
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
	Intent           claimproducer.Intent
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
