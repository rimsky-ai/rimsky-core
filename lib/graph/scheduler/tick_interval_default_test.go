// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scheduler

import (
	"testing"
	"time"
)

func TestResolveTickInterval_ZeroDefaultsTo250ms(t *testing.T) {
	t.Parallel()
	got := resolveTickInterval(0)
	if got != 250*time.Millisecond {
		t.Fatalf("resolveTickInterval(0) = %v, want 250ms (the fast production default) — comparing "+
			"against the literal, not against defaultTickInterval itself, so a regression to the "+
			"headline default value cannot hide behind a self-referential assertion", got)
	}
}

func TestResolveTickInterval_ConfiguredValuePassesThrough(t *testing.T) {
	t.Parallel()
	got := resolveTickInterval(9 * time.Second)
	if got != 9*time.Second {
		t.Fatalf("resolveTickInterval(9s) = %v, want 9s unchanged", got)
	}
}
