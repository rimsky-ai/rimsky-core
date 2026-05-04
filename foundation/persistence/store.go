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
	LockHolders() LockHoldersStore
	NodeAttributes() NodeAttributesStore
	ClaimHolders() ClaimHoldersStore
	Events() EventStore
	Schedules() ScheduleStore
	Supervisors() SupervisorStore
	Frames() FrameStore
	Transaction(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
