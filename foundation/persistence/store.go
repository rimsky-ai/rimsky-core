// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import "context"

// Store is the umbrella interface aggregating every per-feature *Store
// accessor a rimsky process needs. Driver impls (postgres + sqlite) under
// foundation/persistence/<driver>/ return this from Driver.Store(). Most
// callers depend only on a subset of the methods; the umbrella keeps
// wiring code (cmd/* startup) compact.
type Store interface {
	Templates() TemplateStore
	TemplateTags() TemplateTagsStore
	Instances() InstanceStore
	LifecycleIdempotency() LifecycleIdempotencyStore
	Nodes() NodeStore
	ClaimHandles() ClaimHandlesStore
	NodeAttributes() NodeAttributesStore
	ClaimHolders() ClaimHoldersStore
	Events() EventStore
	Schedules() ScheduleStore
	Supervisors() SupervisorStore
	Frames() FrameStore
	BlobOrphans() BlobOrphansStore
	NodeEvents() NodeEventsStore
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
