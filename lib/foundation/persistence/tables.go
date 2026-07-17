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
	NodeRunTree() RunTreeTable

	// @concept: run-scope
	RunScopes() RunScopeTable

	// @concept: api-key
	APIKeys() APIKeyTable

	DeploymentCA() DeploymentCATable

	// @concept: breakpoint
	Breakpoints() BreakpointTable
	BreakpointHits() BreakpointHitTable

	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
