// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import "context"

// Tables is the umbrella interface aggregating every per-row-type Table
// accessor a rimsky process needs. Database impls (postgres + sqlite) under
// foundation/persistence/<driver>/ return this from Database.Tables(). Most
// callers depend only on a subset of the methods; the umbrella keeps
// wiring code (cmd/* startup) compact.
type Tables interface {
	Templates() TemplateTable
	TemplateTags() TemplateTagTable
	Instances() InstanceTable
	LifecycleIdempotency() LifecycleIdempotencyTable
	Nodes() NodeTable
	ClaimHandles() ClaimHandleTable
	NodeAttributes() NodeAttributeTable
	ClaimHolders() ClaimHolderTable
	Events() EventTable
	Supervisors() SupervisorTable
	Frames() FrameTable
	BlobOrphans() BlobOrphanTable
	NodeEvents() NodeEventTable
	WaitSet() WaitSetTable

	// Per-row-type tables introduced by the 2026-05-15 data-platform
	// extensions. Each driver must return a concrete implementation;
	// a nil return is a wiring bug.
	Messages() MessagesTable
	MessageIdempotencies() MessageIdempotencyTable
	Lineage() LineageTable
	PublisherSubscriptions() PublisherSubscriptionsTable
	// RunTree is the parent/child/state accessor on `rimsky_node_runs`.
	// Spec §Run-tree and aggregation.
	RunTree() RunTreeTable

	// RunScopes is the rimsky_run_scopes accessor — first-class
	// execution-context rows per concept:run-scope. Spec
	// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
	RunScopes() RunScopeTable

	// APIKeys is the rimsky_api_keys accessor — Bearer-token auth keys
	// for control-api. Introduced by the 2026-05-15 control-plane MCP
	// and auth spec.
	APIKeys() APIKeyTable

	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
