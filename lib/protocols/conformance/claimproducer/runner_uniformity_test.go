// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"encoding/json"
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
