// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestTerminalOutcomeKey_CommitAlwaysCommitted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cause TerminalCause
	}{
		{"empty", ""},
		{"natural", TerminalCauseNatural},
		{"sibling", TerminalCauseSiblingCancel},
		{"descendant", TerminalCauseDescendantCancel},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := terminalOutcomeKey(TerminalDecision{Outcome: AggregateCommit, Cause: c.cause})
			if got != persistence.LineageOutcomeCommitted {
				t.Errorf("Commit + cause=%q → %q (want %q)",
					c.cause, got, persistence.LineageOutcomeCommitted)
			}
		})
	}
}

func TestTerminalOutcomeKey_AbandonDiscriminatesCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cause   TerminalCause
		wantKey string
	}{
		{"empty_is_natural", "", persistence.LineageOutcomeAbandoned},
		{"natural", TerminalCauseNatural, persistence.LineageOutcomeAbandoned},
		{"sibling_cancel", TerminalCauseSiblingCancel, persistence.LineageOutcomeForceCancelled},
		{"descendant_cancel", TerminalCauseDescendantCancel, persistence.LineageOutcomeForceCancelled},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := terminalOutcomeKey(TerminalDecision{Outcome: AggregateAbandon, Cause: c.cause})
			if got != c.wantKey {
				t.Errorf("Abandon + cause=%q → %q (want %q)", c.cause, got, c.wantKey)
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
