// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// SubscriptionEntry declares one impactee-side reactive coupling.
//
// A subscription is expressed as (sender-selector, signal-type-path,
// optional CEL predicate). See concept:signal for the taxonomy and
// concept:node-subscription for the matching rules.
//
// The cascade walker has one path: in-tx, in-frame. Every match
// stale-marks the receiver inside the sender's frame in the sender's
// settlement tx. Cross-frame coupling is expressed by message-emitter
// nodes (concept:message-emitter-node), not by a per-subscription
// modifier.
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

	// WakeOnChange governs whether a matching emission from the sender
	// dispatches the receiver. true: the cascade walker inserts a wait-
	// set row AND stale-marks the receiver. false: wait-set row only;
	// the receiver is not stale-marked from this edge (it dispatches only
	// when other subscriptions fire it; its substitution context still
	// sees the sender's data if the sender settled in this frame).
	//
	// Required field — no default. Registration rejects entries without
	// an explicit value. See decision:cascade-flags-required-no-defaults.
	//
	//	@concept: cascade
	//	@concept: node-subscription
	WakeOnChange *bool `yaml:"wake_on_change" json:"wake_on_change"`

	// ForceUpstreamRefresh governs whether the receiver's invalidation
	// drags the sender into the same frame for re-evaluation. true: when
	// this receiver is invalidated, the cascade walker also invalidates
	// the sender so it re-runs in the same frame before the receiver
	// dispatches. false: no pull; the receiver dispatches with whatever
	// sender state happens to be in this frame.
	//
	// Required field — no default. Registration rejects entries without
	// an explicit value. A cross-cutting subscription (Instance=true)
	// cannot carry ForceUpstreamRefresh=true; the combination is rejected
	// at registration. See decision:cross-cutting-no-force-upstream-refresh.
	//
	//	@concept: cascade
	//	@concept: node-subscription
	ForceUpstreamRefresh *bool `yaml:"force_upstream_refresh" json:"force_upstream_refresh"`

	// ResolvesViaCallingNode is set by the canonicalizer on
	// subscription edges from non-entry internal sub-graph nodes that
	// reference the entry alias. At runtime the cascade walker
	// resolves such edges to the calling node per-invocation, not to
	// the (absorbed-away) entry alias. Persisted alongside the
	// subscription so the resolution is robust under template
	// re-canonicalization.
	ResolvesViaCallingNode bool `yaml:"resolves_via_calling_node,omitempty" json:"resolves_via_calling_node,omitempty"`
}

// @constraint: rimsky_messages.sender_kind wire values from the 2026-05-17 publisher-protocol unification — still consumed inside signal-message payloads and at the messages endpoint surface; only the per-subscription structured filter retired under the 2026-05-23 signal-taxonomy reshape.
const (
	MessageSenderKindOperator  = "operator"
	MessageSenderKindPublisher = "publisher"
	MessageSenderKindInstance  = "instance"
)

const (
	SubscriptionScopeDirect   = "direct"
	SubscriptionScopeInstance = "instance"
)

// @constraint: redeclared here (not imported from foundation/cascade) so callers — queue probes, cascade walker, audit projections — can avoid crossing the depguard isolation boundary.
const (
	NodeStateFresh   = "fresh"
	NodeStateStale   = "stale"
	NodeStateRunning = "running"
	NodeStateFailed  = "failed"
	NodeStateParked  = "parked"
)

// BoolPtr is the canonical helper for SubscriptionEntry's pointer-bool
// fields. Construction sites pass `spec.BoolPtr(true)` or
// `spec.BoolPtr(false)` inline rather than hoisting a local *bool
// variable. Kept in this file (not in a generic ptr-helper package) so
// its referent is unambiguous in cold reads.
func BoolPtr(v bool) *bool { return &v }
