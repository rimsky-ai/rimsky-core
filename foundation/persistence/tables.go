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
	Schedules() ScheduleTable
	Supervisors() SupervisorTable
	Frames() FrameTable
	BlobOrphans() BlobOrphanTable
	NodeEvents() NodeEventTable
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
