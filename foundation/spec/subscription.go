// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// SubscriptionEntry declares one impactee-side reactive coupling.
// See .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
// Piece 1 (subscription-cascade model resolution).
//
//	@concept: subscription
type SubscriptionEntry struct {
	// Node names the upstream node-type (template-relative). Mutually
	// exclusive with Instance.
	Node string `yaml:"node,omitempty" json:"node,omitempty"`

	// Instance=true makes this a cross-cutting subscription: fires on
	// the topic match across every node in the instance. Mutually
	// exclusive with Node.
	Instance bool `yaml:"instance,omitempty" json:"instance,omitempty"`

	// On is the topic kind: "state" | "attribute" | "event".
	On string `yaml:"on" json:"on"`

	// When narrows a state subscription to a specific node-state
	// ("fresh" | "stale" | "running" | "failed" | "parked"). Empty
	// means "any state transition." Only meaningful when On == "state".
	When string `yaml:"when,omitempty" json:"when,omitempty"`

	// Outcome narrows a state subscription further to a last_outcome
	// value ("fresh_changed" | "fresh_unchanged" | "passed" |
	// "pure_cascade" | "failed"). Only meaningful when On == "state"
	// AND When != "".
	Outcome string `yaml:"outcome,omitempty" json:"outcome,omitempty"`

	// ErrorClass narrows a state subscription further to a specific
	// error_class string. Only meaningful when On == "state" AND
	// When == "failed".
	ErrorClass string `yaml:"error_class,omitempty" json:"error_class,omitempty"`

	// Reason narrows a state subscription further to a specific
	// ParkReason. Lower-snake-case form (matching storage/CLI/Prometheus
	// surface). Only meaningful when On == "state" AND When == "parked".
	Reason string `yaml:"reason,omitempty" json:"reason,omitempty"`

	// Name is required for On == "event" (the named-event name).
	// Optional for On == "attribute" (specific attribute key; absent
	// means "any attribute change"). Unused for On == "state".
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Frame is "in" | "next". Empty defaults to "in" for per-node
	// subscriptions and "next" for cross-cutting (Instance=true).
	Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`
}

// Topic-kind constants for SubscriptionEntry.On.
const (
	TopicKindState     = "state"
	TopicKindAttribute = "attribute"
	TopicKindEvent     = "event"
)

// Subscription-scope constants used by the wait-set persistence layer.
const (
	SubscriptionScopeDirect   = "direct"
	SubscriptionScopeInstance = "instance"
)

// Node-state values valid as SubscriptionEntry.When for On=="state".
// Mirrors the foundation/cascade state machine; redeclared here so the
// template validator can range-check without importing cascade
// (foundation-internal-isolation depguard).
const (
	NodeStateFresh   = "fresh"
	NodeStateStale   = "stale"
	NodeStateRunning = "running"
	NodeStateFailed  = "failed"
	NodeStateParked  = "parked"
)
