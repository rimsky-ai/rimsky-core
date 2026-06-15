// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// TestRunTerminals pins the terminal + retried-terminal idempotency rows
// the suite is contracted to emit per S-conformance-claimproducer-terminals.
//
// The runner MUST drive Commit / Abandon / Release on real claims it Open'd
// and report each as its own pass/fail row, plus a TerminalIdempotency row
// asserting that re-issuing the same terminal verb (same claimID + scope +
// address) is accepted without error and leaves producer state consistent.
//
// Three subtests pin both verdict directions:
//   - honest: an honest producer passes every terminal + idempotency row.
//   - commit_errors: a producer whose Commit returns an error fails the
//     Commit row (FAIL path pinned).
//   - duplicate_terminal_errors: a producer whose duplicate (retried)
//     terminal call errors fails the TerminalIdempotency row.
func TestRunTerminals(t *testing.T) {
	t.Run("honest", func(t *testing.T) {
		results := Run(context.Background(), newFakeProducer())
		for _, name := range []string{"Commit", "Abandon", "Release", "TerminalIdempotency"} {
			r := findRow(t, results, name)
			if r.Err != nil {
				t.Errorf("row %q expected to PASS, got Err: %v", name, r.Err)
			}
		}
	})

	t.Run("commit_errors", func(t *testing.T) {
		fp := newFakeProducer()
		fp.commitErr = fmt.Errorf("synthetic commit failure")
		results := Run(context.Background(), fp)
		r := findRow(t, results, "Commit")
		if r.Err == nil {
			t.Errorf("Commit row expected non-nil Err when producer Commit fails, got PASS")
		}
	})

	t.Run("duplicate_terminal_errors", func(t *testing.T) {
		fp := newFakeProducer()
		fp.failDuplicateTerminal = true
		results := Run(context.Background(), fp)
		r := findRow(t, results, "TerminalIdempotency")
		if r.Err == nil {
			t.Errorf("TerminalIdempotency row expected non-nil Err when a retried terminal verb errors, got PASS")
		}
	})
}

// findRow returns the named conformance row, failing the test if it is
// absent — a missing row is itself a contract violation.
func findRow(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Name)
	}
	t.Fatalf("result set missing the %q row (have: %v)", name, names)
	return CheckResult{}
}

// fakeProducer is a minimal in-memory ClaimProducer that satisfies every
// non-terminal conformance check (Capabilities, EnvelopeNonEmpty, Open,
// Uniformity, and the optional SKIP probes) so Run reaches the terminal +
// idempotency rows. Terminal-verb behavior is injectable so the FAIL paths
// can be pinned.
type fakeProducer struct {
	mu sync.Mutex

	// commitErr, when non-nil, is returned by Commit (first call) — pins
	// the Commit-FAIL row.
	commitErr error

	// failDuplicateTerminal, when true, returns an error on the SECOND
	// invocation of any terminal verb keyed by claimID — pins the
	// TerminalIdempotency-FAIL row (a retried terminal must be accepted).
	failDuplicateTerminal bool

	// terminalCalls counts terminal-verb invocations per claimID so a
	// duplicate (retried) call can be detected.
	terminalCalls map[claimproducer.ClaimID]int
}

func newFakeProducer() *fakeProducer {
	return &fakeProducer{terminalCalls: map[claimproducer.ClaimID]int{}}
}

func (f *fakeProducer) Name() string { return "conformance-target" }

func (f *fakeProducer) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	// @deliberate: advertise only `sync` so the staged_async 9b probe SKIPs
	// and the optional SplitScope / ScopesConflict probes SKIP (their
	// unsupported fallback paths are honored below).
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}, nil
}

func (f *fakeProducer) Open(_ context.Context, _ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	// @deliberate: deterministic, byte-equal scope per Selector so the
	// Uniformity check is exercised and passes. Address mirrors the scope.
	scope, _ := json.Marshal(spec.Selector)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                scope,
			ClaimScope:             scope,
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

func (f *fakeProducer) Commit(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte) (claimproducer.CommitResult, error) {
	if f.commitErr != nil {
		return claimproducer.CommitResult{}, f.commitErr
	}
	return claimproducer.CommitResult{}, f.recordTerminal(claimID)
}

func (f *fakeProducer) Abandon(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte) error {
	return f.recordTerminal(claimID)
}

func (f *fakeProducer) Release(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte) error {
	return f.recordTerminal(claimID)
}

// recordTerminal counts the terminal call for claimID and, when
// failDuplicateTerminal is set, errors on the second (retried) call — the
// dishonest-idempotency behavior the TerminalIdempotency row must catch.
func (f *fakeProducer) recordTerminal(claimID claimproducer.ClaimID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls[claimID]++
	if f.failDuplicateTerminal && f.terminalCalls[claimID] > 1 {
		return fmt.Errorf("duplicate terminal verb rejected for claim %s (non-idempotent)", claimID)
	}
	return nil
}

func (f *fakeProducer) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *fakeProducer) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	// @constraint: byte-equal fallback (the unsupported default per
	// @blessed-invariant 4b) — the conformance SKIP path asserts byte-equal
	// scopes conflict.
	return string(a) == string(b), nil
}
