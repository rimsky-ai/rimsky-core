// main_test.go drives the lifecycle conformance suite against a
// loopback stub store-service started via stores/stub/testfixture.
// Verifies the six lifecycle checks pass against an in-process target,
// pinning the conformance contract for the standard stub store.
package main

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/remote"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

// TestLifecycleConformance_StubStore confirms each of the six
// lifecycle RPCs returns success against a stock stub store-service.
// The stub implements all six as no-ops; that is the published
// contract every store-service must honor.
func TestLifecycleConformance_StubStore(t *testing.T) {
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: store.Capabilities{WriteSemantics: store.WriteSemanticsDirect},
	})
	t.Cleanup(teardown)

	ctx := context.Background()
	client, err := remote.Dial(ctx, "stub", "grpc://"+endpoint)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(client.Close)

	results := RunLifecycleConformance(ctx, client)
	if got, want := len(results), 6; got != want {
		t.Fatalf("RunLifecycleConformance: got %d results, want %d", got, want)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
}
