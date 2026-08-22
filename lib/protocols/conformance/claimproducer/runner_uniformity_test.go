// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type leakTrackingProducer struct {
	mu       sync.Mutex
	opened   map[claimproducer.ClaimID]bool
	released map[claimproducer.ClaimID]bool
}

func newLeakTrackingProducer() *leakTrackingProducer {
	return &leakTrackingProducer{
		opened:   map[claimproducer.ClaimID]bool{},
		released: map[claimproducer.ClaimID]bool{},
	}
}

func (f *leakTrackingProducer) Name() string { return "conformance-target" }

func (f *leakTrackingProducer) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}, nil
}

func (f *leakTrackingProducer) Open(_ context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	scope, _ := json.Marshal(spec.Selector)
	f.mu.Lock()
	f.opened[claimID] = true
	f.mu.Unlock()
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                scope,
			ClaimScope:             scope,
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

func (f *leakTrackingProducer) Commit(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, _ string) (claimproducer.CommitResult, error) {
	f.markReleased(claimID)
	return claimproducer.CommitResult{}, nil
}

func (f *leakTrackingProducer) Abandon(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, _ string) error {
	f.markReleased(claimID)
	return nil
}

func (f *leakTrackingProducer) Release(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, _ string) error {
	f.markReleased(claimID)
	return nil
}

func (f *leakTrackingProducer) markReleased(claimID claimproducer.ClaimID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released[claimID] = true
}

func (f *leakTrackingProducer) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *leakTrackingProducer) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}

func TestRun_UniformityProbeClaimsAreTerminated(t *testing.T) {
	fp := newLeakTrackingProducer()
	_ = Run(context.Background(), fp)

	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.opened) == 0 {
		t.Fatal("test setup: expected the uniformity probe to Open at least one claim")
	}
	for id := range fp.opened {
		if !fp.released[id] {
			t.Errorf("claim %s opened by the conformance runner was never Committed/Abandoned/Released", id)
		}
	}
}

type uniformityFake struct {
	allowed                         []claimproducer.WriteSemantics
	firstSemantics, secondSemantics claimproducer.WriteSemantics

	mu    sync.Mutex
	calls int
}

func (f *uniformityFake) Name() string { return "conformance-target" }

func (f *uniformityFake) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{WriteSemanticsAllowed: f.allowed}, nil
}

func (f *uniformityFake) Open(_ context.Context, _ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	scope, _ := json.Marshal(spec.Selector)
	f.mu.Lock()
	f.calls++
	sem := f.firstSemantics
	if f.calls > 1 {
		sem = f.secondSemantics
	}
	f.mu.Unlock()
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                scope,
			ClaimScope:             scope,
			RealizedWriteSemantics: sem,
		},
	}, nil
}

func (f *uniformityFake) Commit(context.Context, claimproducer.ClaimID, []byte, []byte, string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (f *uniformityFake) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *uniformityFake) Release(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *uniformityFake) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *uniformityFake) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}

func TestRun_OpenFirst_RejectsWriteSemanticsUnknown(t *testing.T) {
	fake := &uniformityFake{
		allowed:         []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		firstSemantics:  claimproducer.WriteSemanticsUnknown,
		secondSemantics: claimproducer.WriteSemanticsUnknown,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "OpenFirst")
	if row.Err == nil {
		t.Fatal("OpenFirst expected non-nil Err when RealizedWriteSemantics is empty/UNKNOWN, got PASS")
	}
	if !strings.Contains(row.Err.Error(), "UNKNOWN") {
		t.Fatalf("OpenFirst error should name the UNKNOWN semantics, got: %v", row.Err)
	}
}

func TestRun_OpenFirst_RejectsSemanticsOutsideEnvelope(t *testing.T) {
	fake := &uniformityFake{
		allowed:         []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		firstSemantics:  claimproducer.WriteSemanticsStagedAsync,
		secondSemantics: claimproducer.WriteSemanticsStagedAsync,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "OpenFirst")
	if row.Err == nil {
		t.Fatal("OpenFirst expected non-nil Err when RealizedWriteSemantics is outside the advertised envelope, got PASS")
	}
	if !strings.Contains(row.Err.Error(), "not in advertised envelope") {
		t.Fatalf("OpenFirst error should name the envelope violation, got: %v", row.Err)
	}
}

func TestRun_Uniformity_RejectsDivergentSemanticsOnByteEqualScope(t *testing.T) {
	fake := &uniformityFake{
		allowed: []claimproducer.WriteSemantics{
			claimproducer.WriteSemanticsSync,
			claimproducer.WriteSemanticsStagedAsync,
		},
		firstSemantics:  claimproducer.WriteSemanticsSync,
		secondSemantics: claimproducer.WriteSemanticsStagedAsync,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "Uniformity")
	if row.Err == nil {
		t.Fatal("Uniformity expected non-nil Err when a byte-equal scope produces divergent RealizedWriteSemantics, got PASS")
	}
	if !strings.Contains(row.Err.Error(), "did not produce identical RealizedWriteSemantics") {
		t.Fatalf("Uniformity error should name the divergence, got: %v", row.Err)
	}
}

func TestRun_Uniformity_PassesIdenticalSemanticsOnByteEqualScope(t *testing.T) {
	fake := &uniformityFake{
		allowed:         []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		firstSemantics:  claimproducer.WriteSemanticsSync,
		secondSemantics: claimproducer.WriteSemanticsSync,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "Uniformity")
	if row.Err != nil {
		t.Fatalf("Uniformity expected PASS when byte-equal scope produces identical RealizedWriteSemantics, got Err: %v", row.Err)
	}
}

type pickPolicyFake struct {
	mu    sync.Mutex
	calls int
}

func (f *pickPolicyFake) Name() string { return "conformance-target" }

func (f *pickPolicyFake) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}, nil
}

func (f *pickPolicyFake) Open(_ context.Context, _ claimproducer.ClaimID, _ claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	f.mu.Lock()
	f.calls++
	scope := []byte(fmt.Sprintf(`{"picked":%d}`, f.calls))
	f.mu.Unlock()
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                scope,
			ClaimScope:             scope,
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

func (f *pickPolicyFake) Commit(context.Context, claimproducer.ClaimID, []byte, []byte, string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (f *pickPolicyFake) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *pickPolicyFake) Release(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *pickPolicyFake) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *pickPolicyFake) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}

// @concept: conformance
func TestRun_Uniformity_SkippedForAPickPolicyProducerWhoseOpensReturnDifferentScopes(t *testing.T) {
	results := Run(context.Background(), &pickPolicyFake{})

	if row := findRow(t, results, "OpenFirst"); row.Err != nil {
		t.Fatalf("OpenFirst expected PASS for a pick-policy producer, got Err: %v", row.Err)
	}
	if row := findRow(t, results, "OpenSecond"); row.Err != nil {
		t.Fatalf("OpenSecond expected PASS for a pick-policy producer, got Err: %v", row.Err)
	}
	for _, r := range results {
		if r.Name == "Uniformity" {
			t.Fatalf("a producer whose consecutive opens pick different scopes has no byte-equal scope "+
				"to compare, so the uniformity check is skipped rather than reported; got a %q row (err=%v)",
				r.Name, r.Err)
		}
	}
}
