// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main_test.go drives the ClaimProducer conformance suite against a
// loopback stub producer-service started via stores/stub/testfixture.
package main

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/runtime/remote"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestClaimProducerConformance_StubStore confirms the ClaimProducer
// conformance suite passes against a stock stub producer-service.
// Capabilities returns a singleton [sync] envelope; Open returns
// matching RealizedWriteSemantics; the uniformity invariant holds.
func TestClaimProducerConformance_StubStore(t *testing.T) {
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{
			WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	ctx := context.Background()
	client, err := remote.Dial(ctx, "stub", "grpc://"+endpoint)
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
	fake := storetest.NewFake("no-partitioning", locks.Capabilities{
		WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync},
	})
	// The default Fake.Open echoes the selector as scope/address but
	// leaves RealizedWriteSemantics unset; tighten to the advertised
	// sync semantics for the uniformity probe.
	fake.OpenFunc = func(_ locks.ClaimID, spec locks.ClaimSpec) (locks.OpenOutcome, error) {
		bytes := []byte(`"` + spec.Selector + `"`)
		return locks.OpenOutcome{
			Available: true,
			Result: locks.ClaimResult{
				Address:                bytes,
				Scope:                  bytes,
				RealizedWriteSemantics: locks.WriteSemanticsSync,
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
