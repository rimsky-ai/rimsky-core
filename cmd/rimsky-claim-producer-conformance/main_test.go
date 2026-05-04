// main_test.go drives the ClaimProducer conformance suite against a
// loopback stub producer-service started via stores/stub/testfixture.
package main

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/foundation/integration/remote"
	"github.com/fallguy/rimsky/foundation/locks"
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
			WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync},
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
}
