// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

// NamedLockSpec is the producer-independent named-lock primitive.
// Templates reference named locks by name only; the limit (mutex vs.
// counting) lives in the operator's named_locks: config block.
//
// NamedLockSpec is rimsky-internal — it has no protocol-layer
// equivalent because named locks never cross the wire to a producer.
type NamedLockSpec struct {
	// Name is the concrete, post-substitution lock name (e.g.
	// "review:item-42" from a "review:{{nodes.x.attribute.id}}"
	// template directive). It keys the advisory lock, the counter
	// semaphore, and the claim-handle row.
	Name string
	// TemplateName is the pre-substitution name as declared in the
	// template (the directive text). Bounded cardinality — one value
	// per template declaration — so it is the metrics label; the
	// concrete Name stays in the events ledger only.
	TemplateName string
}
