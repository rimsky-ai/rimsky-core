// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// SubscriptionEntry declares one impactee-side reactive coupling.
//
// Under the 2026-05-23 signal-taxonomy reshape, a subscription is
// expressed as (sender-selector, signal-type-path, optional CEL
// predicate, frame) — replacing the per-dimension structured filter set
// (`on`/`when`/`outcome`/`error_class`/`reason`/`name`/`kind`/`sender`/
// `sender_kind`/`target`) used pre-2026-05-23. See concept:signal for
// the taxonomy and concept:node-subscription for the matching rules.
//
//	@concept: node-subscription
type SubscriptionEntry struct {
	// Node names the upstream node-type (template-relative). Mutually
	// exclusive with Instance.
	Node string `yaml:"node,omitempty" json:"node,omitempty"`

	// Instance=true makes this a cross-cutting subscription: fires on
	// the type match across every node in the instance. Mutually
	// exclusive with Node.
	Instance bool `yaml:"instance,omitempty" json:"instance,omitempty"`

	// Type is the canonical signal type-path the subscription matches
	// (exact or trailing-`*` prefix per concept:signal). Required.
	// Validated at registration via signal.ValidateSubscriptionType.
	Type string `yaml:"type" json:"type"`

	// When is an optional CEL expression evaluated against the matched
	// signal's payload. Empty means "match any payload." Validated +
	// compiled at registration via signal.CompileWhen, which parse-
	// checks field references against the resolved payload schema for
	// exact-type subscriptions and binds payload as dyn for prefix-type
	// subscriptions.
	When string `yaml:"when,omitempty" json:"when,omitempty"`

	// Frame is "in" | "next". Empty defaults to "in" for per-node
	// subscriptions and "next" for cross-cutting (Instance=true).
	Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`

	// ResolvesViaCallingNode is set by the canonicalizer on
	// subscription edges from non-entry internal sub-graph nodes that
	// reference the entry alias. At runtime the cascade walker
	// resolves such edges to the calling node per-invocation, not to
	// the (absorbed-away) entry alias. Persisted alongside the
	// subscription so the resolution is robust under template
	// re-canonicalization. Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Sub-graphs / Identity and absorption + §Multiple invocations.
	ResolvesViaCallingNode bool `yaml:"resolves_via_calling_node,omitempty" json:"resolves_via_calling_node,omitempty"`
}

// MessageSenderKind values for rimsky_messages.sender_kind. Per spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
//
// Still consumed inside signal-message payloads and at the messages
// endpoint surface; only the per-subscription structured filter
// retired under the 2026-05-23 signal-taxonomy reshape.
const (
	MessageSenderKindOperator  = "operator"
	MessageSenderKindPublisher = "publisher"
	MessageSenderKindInstance  = "instance"
)

// Subscription-scope constants used by the wait-set persistence layer.
const (
	SubscriptionScopeDirect   = "direct"
	SubscriptionScopeInstance = "instance"
)

// Node-state values. Still referenced elsewhere (queue probes,
// cascade walker, audit projections); redeclared here so callers can
// avoid importing foundation/cascade across the depguard isolation
// boundary.
const (
	NodeStateFresh   = "fresh"
	NodeStateStale   = "stale"
	NodeStateRunning = "running"
	NodeStateFailed  = "failed"
	NodeStateParked  = "parked"
)
