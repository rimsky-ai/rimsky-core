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
		"transient/heartbeat_missed",
		"transient/await_async",
		"attribute/budget_cents/changed",
		"event/discovered",
		"message/invalidate/operator/self",
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
		"terminal/error",            // no class leaf
		"lifecycle/node_created",    // explicitly-not-introduced kind
		"",                          // empty
		"attribute/changed",         // no key
		"attribute/foo/bar/changed", // key cannot itself contain '/'
		"transient/retry",           // no params
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
		"event/*",
		"transient/*",
		"transient/retry/*",
		"attribute/*",
		"message/*",
		"terminal/park/snooze", // exact still accepted
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
