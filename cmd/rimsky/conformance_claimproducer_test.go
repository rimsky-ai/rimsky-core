// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	cpconformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestClaimProducerConformance_StubStore(t *testing.T) {
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	ctx := context.Background()
	client, err := peer.Dial(ctx, "stub", "grpc://"+endpoint, peer.TLSModeOff)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(client.Close)

	results := cpconformance.Run(ctx, client)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	wantNames := map[string]bool{
		"SplitScopeListReturnsAllElements": false,
		"SplitScopePreservesPartitionKey":  false,
		"SplitScopePreservesPayload":       false,
		"SplitScopeAddressFieldPresent":    false,
		"ScopesConflict":                   false,
	}
	seenCounts := make(map[string]int, len(results))
	for _, r := range results {
		seenCounts[r.Name]++
		if _, ok := wantNames[r.Name]; ok {
			wantNames[r.Name] = true
		}
	}
	for name, count := range seenCounts {
		if count > 1 {
			t.Errorf("conformance result row %q appears %d times; check names are required-unique", name, count)
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("expected check %q to run against stub-store, did not see it in results", name)
		}
	}
}

func TestClaimProducerConformance_NoPartitioning(t *testing.T) {
	fake := storetest.NewFake("no-partitioning", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
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
	results := cpconformance.Run(context.Background(), fake)
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
