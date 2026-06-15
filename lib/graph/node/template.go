// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Template DSL types (spec §18). The graph-author's view of a node:
// stores it interacts with, named locks it holds, attributes it
// declares, and `holds:` edges for held claims it co-holds downstream.
//
// The persistable row-type primitives moved into foundation/spec so
// foundation can be self-contained (foundation never imports graph).
// This file re-exports those types as aliases so existing graph/node
// consumers keep working unchanged. The algorithms that operate on
// these types — HoldingSubgraphsForTemplate, RequiredStores, the
// template validator — remain in this package.
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
//   - TemplateNodeDef carries Holds map[string]HoldsBinding for
//     co-holders downstream of an acquirer (concept:claim-co-holdership).

package node

import "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"

// @deliberate: Row-type aliases — the canonical definitions live in
// foundation/spec. These aliases keep `node.TemplateSpec` etc. working
// for existing call sites without rewriting every importer.
//
// Lifecycle-handler types (`OnAcquireUnavailableHandler`,
// `OnExecutorCompleteHandler`, `OnExecutorTerminalHandler`) retired
// policy-decoupling-design.md`.
type (
	TemplateSpec      = spec.TemplateSpec
	TemplateNodeDef   = spec.TemplateNodeDef
	NodeStoreRef      = spec.NodeStoreRef
	NodeLockRef       = spec.NodeLockRef
	NodeAttributesDef = spec.NodeAttributesDef
	SubscriptionEntry = spec.SubscriptionEntry

	// @deliberate: data-platform extensions row-types re-exported here
	// so existing call sites do not need to import foundation/spec
	// directly.
	GraphSpec         = spec.GraphSpec
	HoldsBinding      = spec.HoldsBinding
	FanOutSpec        = spec.FanOutSpec
	PublisherSpec     = spec.PublisherSpec
	AggregationPolicy = spec.AggregationPolicy

	// @deliberate: multi-instance-template-ergonomics additions —
	// template-author attribute defaults (L1 in the override merge).
	TemplateDefaults          = spec.TemplateDefaults
	TemplateAttributeDefaults = spec.TemplateAttributeDefaults
)

// BoolPtr re-exports spec.BoolPtr so test fixtures importing only the
// node package can construct WakeOnChange and ForceUpstreamRefresh
// inline without adding a foundation/spec import. Behavior identical to
// spec.BoolPtr.
var BoolPtr = spec.BoolPtr

// @deliberate: Frame-resolution constants re-exported from foundation/spec.
const (
	FrameResolutionCoalesce    = spec.FrameResolutionCoalesce
	FrameResolutionSerialQueue = spec.FrameResolutionSerialQueue
	FrameTimeoutDefaultMs      = spec.FrameTimeoutDefaultMs
	FrameTimeoutMinMs          = spec.FrameTimeoutMinMs

	// @deliberate: MainGraphName mirrors the reserved graph name for the
	// top-level graph in a multi-graph template.
	MainGraphName = spec.MainGraphName
)

// @deliberate: Frame + target constants. The lifecycle-handler resolve vocabulary
// retired 2026-05-23 alongside the handler types; ErrorPolicy's 4-value
// action vocabulary (`pass | give_up | retry |
// discard_claims_then_retry`) is the operator-facing replacement and
// lives on `concept:error-policy`.
const (
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
