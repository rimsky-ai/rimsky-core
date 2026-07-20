// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestTerminalOutcomeKey_MapsEachOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		outcome TerminalOutcome
		wantKey string
	}{
		{"commit", OutcomeCommit, persistence.LineageOutcomeCommitted},
		{"abandon", OutcomeAbandon, persistence.LineageOutcomeAbandoned},
		{"abandon_sibling_cancel", OutcomeAbandonSiblingCancel, persistence.LineageOutcomeForceCancelled},
		{"abandon_descendant_cancel", OutcomeAbandonDescendantCancel, persistence.LineageOutcomeForceCancelled},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := terminalOutcomeKey(TerminalDecision{Outcome: c.outcome})
			if got != c.wantKey {
				t.Errorf("%v → %q (want %q)", c.outcome, got, c.wantKey)
			}
		})
	}
}

func TestTerminalOutcome_IsAbandonAndCauseString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		outcome   TerminalOutcome
		isAbandon bool
		cause     string
	}{
		{OutcomeCommit, false, ""},
		{OutcomeAbandon, true, "natural"},
		{OutcomeAbandonSiblingCancel, true, "sibling_failed"},
		{OutcomeAbandonDescendantCancel, true, "parent_resolved"},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.outcome), func(t *testing.T) {
			t.Parallel()
			if got := c.outcome.IsAbandon(); got != c.isAbandon {
				t.Errorf("%v.IsAbandon() = %t (want %t)", c.outcome, got, c.isAbandon)
			}
			if got := c.outcome.CauseString(); got != c.cause {
				t.Errorf("%v.CauseString() = %q (want %q)", c.outcome, got, c.cause)
			}
		})
	}
}

func TestPreferVersionID_VerbWinsOverHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		verb string
		hint string
		want string
	}{
		{"verb_wins", "v-new", "v-old", "v-new"},
		{"hint_fallback", "", "v-old", "v-old"},
		{"both_empty", "", "", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := preferVersionID(c.verb, c.hint); got != c.want {
				t.Errorf("preferVersionID(%q, %q) = %q (want %q)", c.verb, c.hint, got, c.want)
			}
		})
	}
}
