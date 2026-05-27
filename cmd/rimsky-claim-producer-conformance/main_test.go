// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// main_test.go drives the ClaimProducer conformance suite against a
// loopback stub producer-service started via stores/stub/testfixture.
package main

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	peer "github.com/rimsky-ai/rimsky-core/runtime/peer"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

// TestClaimProducerConformance_StubStore confirms the ClaimProducer
// conformance suite passes against a stock stub producer-service.
// Capabilities returns a singleton [sync] envelope; Open returns
// matching RealizedWriteSemantics; the uniformity invariant holds.
func TestClaimProducerConformance_StubStore(t *testing.T) {
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	ctx := context.Background()
	client, err := peer.Dial(ctx, "stub", "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(client.Close)

	results := RunClaimProducerConformance(ctx, client)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	// Stub-store advertises both partitioning capabilities (per M
	// dispatch / O1 fixture), so SplitScope + ScopesConflict must run
	// as full checks — NOT the Skipped variants.
	wantNames := map[string]bool{"SplitScope": false, "ScopesConflict": false}
	for _, r := range results {
		if _, ok := wantNames[r.Name]; ok {
			wantNames[r.Name] = true
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("expected check %q to run against stub-store, did not see it in results", name)
		}
	}
}

// TestClaimProducerConformance_NoPartitioning probes the skip path:
// a producer that does not advertise SupportsSplitScope or
// SupportsScopesConflict still passes conformance; the optional
// checks surface as SplitScopeSkipped / ScopesConflictSkipped.
func TestClaimProducerConformance_NoPartitioning(t *testing.T) {
	fake := storetest.NewFake("no-partitioning", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	// The default Fake.Open echoes the selector as scope/address but
	// leaves RealizedWriteSemantics unset; tighten to the advertised
	// sync semantics for the uniformity probe.
	fake.OpenFunc = func(_ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
		bytes := []byte(`"` + spec.Selector + `"`)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address:                bytes,
				ClaimScope:             bytes,
				RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
			},
		}, nil
	}
	results := RunClaimProducerConformance(context.Background(), fake)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	wantSkips := map[string]bool{"SplitScopeSkipped": false, "ScopesConflictSkipped": false}
	for _, r := range results {
		if _, ok := wantSkips[r.Name]; ok {
			wantSkips[r.Name] = true
		}
	}
	for name, seen := range wantSkips {
		if !seen {
			t.Errorf("expected SKIP check %q against no-partitioning producer, did not see it", name)
		}
	}
}
