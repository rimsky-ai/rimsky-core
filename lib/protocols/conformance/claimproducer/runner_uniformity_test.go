// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"encoding/json"
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
