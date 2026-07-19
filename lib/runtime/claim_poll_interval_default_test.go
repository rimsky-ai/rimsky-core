// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"
	"time"
)

func TestResolveClaimPollInterval_ZeroDefaultsTo200ms(t *testing.T) {
	t.Parallel()
	got := resolveClaimPollInterval(0)
	if got != 200*time.Millisecond {
		t.Fatalf("resolveClaimPollInterval(0) = %v, want 200ms (the fast production default) — "+
			"comparing against the literal, not against defaultClaimPollInterval itself, so a "+
			"regression to the headline default value cannot hide behind a self-referential assertion", got)
	}
}

func TestResolveClaimPollInterval_ConfiguredValuePassesThrough(t *testing.T) {
	t.Parallel()
	got := resolveClaimPollInterval(3 * time.Second)
	if got != 3*time.Second {
		t.Fatalf("resolveClaimPollInterval(3s) = %v, want 3s unchanged", got)
	}
}
