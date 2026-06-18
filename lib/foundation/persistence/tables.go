// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import "context"

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
	WaitSet() WaitSetTable

	Messages() MessagesTable
	MessageIdempotencies() MessageIdempotencyTable
	Lineage() LineageTable
	PublisherSubscriptions() PublisherSubscriptionsTable
	RunTree() RunTreeTable

	// @concept: run-scope — first-class execution-context rows on rimsky_run_scopes.
	RunScopes() RunScopeTable

	// @concept: api-key — Bearer-token auth keys for control-api on rimsky_api_keys.
	APIKeys() APIKeyTable

	// @concept: breakpoint — accessors on rimsky_breakpoints / rimsky_breakpoint_hits.
	Breakpoints() BreakpointTable
	BreakpointHits() BreakpointHitTable

	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
