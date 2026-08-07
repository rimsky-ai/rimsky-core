// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
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

func TestResolveClaimHandleTerminal_RejectsUnknownOutcomeBeforeAnyProducerVerb(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := storetest.NewFake("bogus-outcome-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})

	_, err := ResolveClaimHandleTerminal(ctx, RunArgs{}, TerminalDecision{
		ClaimHandleID: shared.UUID{},
		Producer:      store,
		Outcome:       TerminalOutcome(""),
	}, nil)
	if err == nil {
		t.Fatalf("expected an error for a zero-value TerminalOutcome; a garbage/unset outcome must not " +
			"be silently classified as an abandon")
	}

	for _, c := range store.Calls() {
		t.Errorf("unknown-outcome decision must not reach the producer at all; got verb %q", c.Verb)
	}
}
