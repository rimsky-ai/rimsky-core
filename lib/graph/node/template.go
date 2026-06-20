// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"

type (
	TemplateSpec         = spec.TemplateSpec
	TemplateNodeDef      = spec.TemplateNodeDef
	NodeClaimProducerRef = spec.NodeClaimProducerRef
	NodeLockRef          = spec.NodeLockRef
	NodeAttributesDef    = spec.NodeAttributesDef
	SubscriptionEntry    = spec.SubscriptionEntry

	GraphSpec         = spec.GraphSpec
	HoldsBinding      = spec.HoldsBinding
	FanOutSpec        = spec.FanOutSpec
	PublisherSpec     = spec.PublisherSpec
	AggregationPolicy = spec.AggregationPolicy

	TemplateDefaults          = spec.TemplateDefaults
	TemplateAttributeDefaults = spec.TemplateAttributeDefaults

	MessageSchema = spec.MessageSchema
)

var BoolPtr = spec.BoolPtr

const (
	FrameTimeoutDefaultMs = spec.FrameTimeoutDefaultMs
	FrameTimeoutMinMs     = spec.FrameTimeoutMinMs

	MainGraphName = spec.MainGraphName
)

const (
	SelfTarget = spec.SelfTarget
)

func RequiredClaimProducers(node TemplateNodeDef) []string {
	if len(node.ClaimProducers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(node.ClaimProducers))
	out := make([]string, 0, len(node.ClaimProducers))
	for _, s := range node.ClaimProducers {
		if s.Name == "" {
			continue
		}
		if _, ok := seen[s.Name]; ok {
			continue
		}
		seen[s.Name] = struct{}{}
		out = append(out, s.Name)
	}
	return out
}
