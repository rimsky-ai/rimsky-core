// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	// @concept: service-address-book
	ServiceAddressBook() ServiceAddressBookTable
	Frames() FrameTable
	BlobOrphans() BlobOrphanTable
	WaitSet() WaitSetTable

	Messages() MessageTable
	MessageIdempotencies() MessageIdempotencyTable
	Lineage() LineageTable
	PublisherSubscriptions() PublisherSubscriptionTable
	NodeRunTree() NodeRunTreeTable

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
