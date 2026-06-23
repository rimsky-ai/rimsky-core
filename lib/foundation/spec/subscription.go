// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

// @concept: node-subscription
type SubscriptionEntry struct {
	Node string `yaml:"node,omitempty" json:"node,omitempty"`

	Instance bool `yaml:"instance,omitempty" json:"instance,omitempty"`

	Type string `yaml:"type" json:"type"`

	When string `yaml:"when,omitempty" json:"when,omitempty"`

	//	@concept: cascade
	//	@concept: node-subscription
	ForceUpstreamRefresh *bool `yaml:"force_upstream_refresh" json:"force_upstream_refresh"`

	ResolvesViaCallingNode bool `yaml:"resolves_via_calling_node,omitempty" json:"resolves_via_calling_node,omitempty"`
}

const (
	MessageSenderKindOperator  = "operator"
	MessageSenderKindPublisher = "publisher"
	MessageSenderKindInstance  = "instance"
)

const (
	SubscriptionScopeDirect   = "direct"
	SubscriptionScopeInstance = "instance"
)

const (
	NodeStateFresh   = "fresh"
	NodeStateStale   = "stale"
	NodeStateRunning = "running"
	NodeStateFailed  = "failed"
	NodeStateParked  = "parked"
)

func BoolPtr(v bool) *bool { return &v }

func IntPtr(v int) *int { return &v }
