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
		"transient/infra/heartbeat_lost",
		"transient/release_and_requeue/lock_lost",
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
