// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package signal

import (
	"strings"
	"testing"
)

func TestValidateTypePath_AcceptsCanonical(t *testing.T) {
	cases := []TypePath{
		"terminal/success",
		"terminal/error/http/timeout",
		"terminal/error/foo",
		"transient/park",
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
		"transient/park/snooze",
		"transient/park/await_callback",
		"terminal/park/snooze",
		"terminal/park/await_callback",
		"terminal/infra/heartbeat_lost",
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
		"attribute/*",
		"terminal/success",
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

func TestValidateSubscriptionType_RejectsTransientTargets(t *testing.T) {
	cases := []TypePath{
		"transient/*",
		"transient/retry/*",
		"transient/retry/1/foo",
		"transient/infra/*",
		"transient/infra/heartbeat_lost",
		"transient/release_and_requeue/*",
		"transient/release_and_requeue/lock_lost",
		"transient/await_async",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			err := ValidateSubscriptionType(c)
			if err == nil {
				t.Fatalf("ValidateSubscriptionType(%q) returned nil; expected error — "+
					"transient signals do not cascade and are not subscribable", c)
			}
			if !strings.Contains(err.Error(), string(c)) {
				t.Fatalf("ValidateSubscriptionType(%q) error %q does not name the rejected type", c, err.Error())
			}
		})
	}
}

func TestShouldCascade(t *testing.T) {
	cascading := []TypePath{
		"terminal/success",
		"terminal/error/foo",
		"attribute/budget_cents/changed",
	}
	for _, c := range cascading {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if !ShouldCascade(c) {
				t.Fatalf("ShouldCascade(%q) = false; expected true", c)
			}
		})
	}

	nonCascading := []TypePath{
		"transient/retry/1/foo",
		"transient/infra/heartbeat_lost",
		"transient/release_and_requeue/lock_lost",
		"transient/await_async",
		"transient/park",
	}
	for _, c := range nonCascading {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if ShouldCascade(c) {
				t.Fatalf("ShouldCascade(%q) = true; expected false", c)
			}
		})
	}
}

func TestValidateSubscriptionType_RejectsExplicitParkTargets(t *testing.T) {
	cases := []TypePath{
		"transient/park",
		"transient/park/*",
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			if err := ValidateSubscriptionType(c); err == nil {
				t.Fatalf("ValidateSubscriptionType(%q) returned nil; expected park-audit-only error", c)
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
