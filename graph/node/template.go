// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Template DSL types (spec §18). The graph-author's view of a node:
// stores it interacts with, named locks it holds, attributes it
// declares, and inheritance edges for held claims it consumes
// downstream.
//
// The persistable row-type primitives moved into foundation/spec so
// foundation can be self-contained (foundation never imports graph).
// This file re-exports those types as aliases so existing graph/node
// consumers keep working unchanged. The algorithms that operate on
// these types — ValidateInheritance, HoldingSubgraphsForTemplate,
// RequiredStores, the template validator — remain in this package.
//
// History (informational):
//   - Stores-redesign-v2 dropped per-claim on_commit/on_give_up
//     overrides from claim entries and added a `claim_resolutions`
//     map on the acquiring node.
//   - The 2026-04-30 stores cleanup
//     (`docs/history/2026-04-30-stores-protocol-cleanup-design.md`)
//     removes `claim_resolutions` entirely. Store disposition
//     (what Commit / Abandon mean for the store's own state) is
//     governed entirely by per-store config; rimsky carries only
//     the success/failure binary (success → Commit; failure →
//     Abandon).
//   - NodeStoreRef carries selector + intent + alias.
//   - TemplateNodeDef carries Inherits []InheritEntry for held-claim
//     consumers downstream of an acquirer.

package node

import "github.com/fallguy/rimsky/foundation/spec"

// Row-type aliases — the canonical definitions live in
// foundation/spec. These aliases keep `node.TemplateSpec` etc. working
// for existing call sites without rewriting every importer.
type (
	TemplateSpec                = spec.TemplateSpec
	TemplateNodeDef             = spec.TemplateNodeDef
	NodeStoreRef                = spec.NodeStoreRef
	NodeLockRef                 = spec.NodeLockRef
	NodeAttributesDef           = spec.NodeAttributesDef
	InheritEntry                = spec.InheritEntry
	SubscriptionEntry           = spec.SubscriptionEntry
	OnAcquireUnavailableHandler = spec.OnAcquireUnavailableHandler
	OnExecutorCompleteHandler   = spec.OnExecutorCompleteHandler
	OnExecutorTerminalHandler   = spec.OnExecutorTerminalHandler

	// 2026-05-15 data-platform extensions row-types re-exported here
	// so existing call sites do not need to import foundation/spec
	// directly.
	GraphSpec         = spec.GraphSpec
	HoldsBinding      = spec.HoldsBinding
	FanOutSpec        = spec.FanOutSpec
	PublisherSpec     = spec.PublisherSpec
	AggregationPolicy = spec.AggregationPolicy
)

// Frame-resolution constants re-exported from foundation/spec.
const (
	FrameResolutionCoalesce    = spec.FrameResolutionCoalesce
	FrameResolutionSerialQueue = spec.FrameResolutionSerialQueue
	FrameTimeoutDefaultMs      = spec.FrameTimeoutDefaultMs
	FrameTimeoutMinMs          = spec.FrameTimeoutMinMs

	// MainGraphName mirrors the reserved graph name for the top-level
	// graph in a multi-graph template. Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Sub-graphs / Identity.
	MainGraphName = spec.MainGraphName
)

// Resolve constants per handler. The validator at template-deploy
// rejects out-of-vocabulary combinations.
const (
	ResolvePass            = spec.ResolvePass
	ResolveRetry           = spec.ResolveRetry
	ResolveError           = spec.ResolveError
	ResolveByChanged       = spec.ResolveByChanged
	ResolveAlwaysPropagate = spec.ResolveAlwaysPropagate
	ResolveNeverPropagate  = spec.ResolveNeverPropagate

	FrameIn   = spec.FrameIn
	FrameNext = spec.FrameNext

	SelfTarget = spec.SelfTarget
)

// RequiredStores returns the distinct store names referenced by
// node.Stores, preserving first-seen order. Used by enqueue logic to
// populate rimsky_node_runs.required_stores.
func RequiredStores(node TemplateNodeDef) []string {
	if len(node.Stores) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(node.Stores))
	out := make([]string, 0, len(node.Stores))
	for _, s := range node.Stores {
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
