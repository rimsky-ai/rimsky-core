// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import "testing"

func TestValidateTypePath_AcceptsCanonical(t *testing.T) {
	cases := []TypePath{
		"terminal/success",
		"terminal/error/http/timeout",
		"terminal/error/foo",
		"terminal/park/snooze",
		"terminal/park/await_callback",
		"terminal/infra/heartbeat_lost",
		"transient/retry/3/agent/rate_limited",
		"transient/retry/1/foo",
		"transient/await_async",
		"attribute/budget_cents/changed",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateTypePath(c); err != nil {
				t.Fatalf("ValidateTypePath(%q) returned %v; expected nil", c, err)
			}
		})
	}
}

func TestValidateTypePath_RejectsUnknown(t *testing.T) {
	cases := []TypePath{
		"terminal/garbage",
		"not_a_kind/foo",
		"terminal/error",
		"lifecycle/node_created",
		"",
		"attribute/changed",
		"attribute/foo/bar/changed",
		"transient/retry",
		"message/invalidate/operator/foo",
		"message/anything",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateTypePath(c); err == nil {
				t.Fatalf("ValidateTypePath(%q) returned nil; expected error", c)
			}
		})
	}
}

func TestValidateSubscriptionType_AcceptsTrailingWildcard(t *testing.T) {
	cases := []TypePath{
		"terminal/error/*",
		"terminal/*",
		"transient/*",
		"transient/retry/*",
		"attribute/*",
		"terminal/park/snooze",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateSubscriptionType(c); err != nil {
				t.Fatalf("ValidateSubscriptionType(%q) returned %v; expected nil", c, err)
			}
		})
	}
}

func TestValidateSubscriptionType_RejectsPositionalWildcard(t *testing.T) {
	cases := []TypePath{
		"terminal/*/foo",
		"*/error/*",
		"*/foo",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateSubscriptionType(c); err == nil {
				t.Fatalf("ValidateSubscriptionType(%q) returned nil; expected error", c)
			}
		})
	}
}

// TestValidateSubscriptionType_RejectsMessageTypePath pins the
// 2026-06-14 message-schema-layer retirement of the `message/*`
// top-level kind. Message arrival is now a virtual-node settle —
// receivers subscribe with `node: <message-type>, type: terminal/success`,
// NOT with `type: message/...`. A subscription that names `message/...`
// as the signal type-path must fail through the canonical-taxonomy
// validator.
func TestValidateSubscriptionType_RejectsMessageTypePath(t *testing.T) {
	cases := []TypePath{
		"message/*",
		"message/invalidate/*",
		"message/invalidate/operator/self",
		"message/invalidate/operator/foo",
		"message/refresh/publisher/bar",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateSubscriptionType(c); err == nil {
				t.Fatalf("ValidateSubscriptionType(%q) returned nil; expected error (message/* taxonomy retired)", c)
			}
		})
	}
}
