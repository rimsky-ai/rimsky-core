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

func TestRunTerminals(t *testing.T) {
	t.Run("honest", func(t *testing.T) {
		results := Run(context.Background(), newFakeProducer())
		for _, name := range []string{
			"Commit", "Abandon", "Release",
			"TerminalIdempotency", "AbandonTerminalIdempotency", "ReleaseTerminalIdempotency",
		} {
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
		for _, name := range []string{"TerminalIdempotency", "AbandonTerminalIdempotency", "ReleaseTerminalIdempotency"} {
			r := findRow(t, results, name)
			if r.Err == nil {
				t.Errorf("%s row expected non-nil Err when a retried terminal verb errors, got PASS", name)
			}
		}
	})

	t.Run("duplicate_abandon_only_fails_abandon_idempotency", func(t *testing.T) {
		fp := newFakeProducer()
		fp.failDuplicateVerb = "abandon"
		results := Run(context.Background(), fp)
		if r := findRow(t, results, "AbandonTerminalIdempotency"); r.Err == nil {
			t.Errorf("AbandonTerminalIdempotency row expected non-nil Err when retried Abandon errors, got PASS")
		}
		for _, name := range []string{"TerminalIdempotency", "ReleaseTerminalIdempotency"} {
			r := findRow(t, results, name)
			if r.Err != nil {
				t.Errorf("%s row expected to PASS when only Abandon retry errors, got Err: %v", name, r.Err)
			}
		}
	})

	t.Run("duplicate_release_only_fails_release_idempotency", func(t *testing.T) {
		fp := newFakeProducer()
		fp.failDuplicateVerb = "release"
		results := Run(context.Background(), fp)
		if r := findRow(t, results, "ReleaseTerminalIdempotency"); r.Err == nil {
			t.Errorf("ReleaseTerminalIdempotency row expected non-nil Err when retried Release errors, got PASS")
		}
		for _, name := range []string{"TerminalIdempotency", "AbandonTerminalIdempotency"} {
			r := findRow(t, results, name)
			if r.Err != nil {
				t.Errorf("%s row expected to PASS when only Release retry errors, got Err: %v", name, r.Err)
			}
		}
	})
}

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

type fakeProducer struct {
	mu sync.Mutex

	commitErr error

	failDuplicateTerminal bool
	failDuplicateVerb     string

	terminalCalls map[claimproducer.ClaimID]int
}

func newFakeProducer() *fakeProducer {
	return &fakeProducer{terminalCalls: map[claimproducer.ClaimID]int{}}
}

func (f *fakeProducer) Name() string { return "conformance-target" }

func (f *fakeProducer) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}, nil
}

func (f *fakeProducer) Open(_ context.Context, _ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
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

func (f *fakeProducer) Commit(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, leaseToken string) (claimproducer.CommitResult, error) {
	if f.commitErr != nil {
		return claimproducer.CommitResult{}, f.commitErr
	}
	return claimproducer.CommitResult{}, f.recordTerminal("commit", claimID)
}

func (f *fakeProducer) Abandon(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, leaseToken string) error {
	return f.recordTerminal("abandon", claimID)
}

func (f *fakeProducer) Release(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, leaseToken string) error {
	return f.recordTerminal("release", claimID)
}

func (f *fakeProducer) recordTerminal(verb string, claimID claimproducer.ClaimID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls[claimID]++
	if f.terminalCalls[claimID] > 1 && (f.failDuplicateTerminal || f.failDuplicateVerb == verb) {
		return fmt.Errorf("duplicate terminal verb rejected for claim %s (non-idempotent)", claimID)
	}
	return nil
}

func (f *fakeProducer) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *fakeProducer) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}
