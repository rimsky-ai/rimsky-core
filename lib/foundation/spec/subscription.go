// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package spec

// @concept: node-subscription
type SubscriptionEntry struct {
	Node string `yaml:"node,omitempty" json:"node,omitempty"`

	Type string `yaml:"type" json:"type"`

	When string `yaml:"when,omitempty" json:"when,omitempty"`

	// @concept: cascade
	// @concept: node-subscription
	// @decision: cascade-flags-on-subscribes
	ForceUpstreamRefresh *bool `yaml:"force_upstream_refresh" json:"force_upstream_refresh"`

	ResolvesViaCallingNode bool `yaml:"resolves_via_calling_node,omitempty" json:"resolves_via_calling_node,omitempty"`
}
