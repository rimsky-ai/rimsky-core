// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pure-Go unit tests for the small helpers in terminal_decision.go that
// don't require a real Postgres harness. The heavy paths (the full
// ResolveClaimHandleTerminal flow + the cancel walkers) are exercised
// by the scenario tests in test/scenarios/lineage/ and test/scenarios/
// forensics/ + the in-process tests in auto_terminal_test.go.

package runtime

import (
	"testing"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
)

// TestTerminalOutcomeKey_CommitAlwaysCommitted pins that Commit
// resolutions ignore the Cause field and always produce
// `outcome: committed`. A force-cancelled Commit makes no sense (the
// cancel walkers only fire Abandon), but the helper must still default
// safely.
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

// TestTerminalOutcomeKey_AbandonDiscriminatesCause pins the
// natural-vs-force-cancelled discrimination. The sibling- and
// descendant-cancel causes both promote to `force_cancelled`; natural
// (or empty) stays `abandoned`.
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

// TestPreferVersionID_VerbWinsOverHint pins the version-ID preference:
// a freshly-returned candidate version always wins over the previously-
// hinted one. Falls back to the hint when the verb didn't produce a
// version (e.g. on a non-DataProcessing producer).
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
