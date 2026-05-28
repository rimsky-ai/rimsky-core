// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// conformance_dataprocessing_test.go drives the DataProcessing conformance
// suite against the stub store-service's DataProcessing extension started via
// stores/stub/testfixture. Migrated from the former
// cmd/rimsky-data-processing-conformance/main_test.go; it exercises the
// importable lib/protocols/conformance/dataprocessing.Run directly.
package main

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	dpconformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/dataprocessing"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestDataProcessingConformance_StubStore confirms the
// DataProcessing conformance suite passes against the stub
// store-service's in-memory DataProcessing extension.
func TestDataProcessingConformance_StubStore(t *testing.T) {
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewDataProcessingClient(conn)

	results := dpconformance.Run(context.Background(), client)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
}
