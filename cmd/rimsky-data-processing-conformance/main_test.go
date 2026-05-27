// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main_test.go drives the DataProcessing conformance suite against
// the stub store-service's DataProcessing extension started via
// stores/stub/testfixture.

package main

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
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

	results := RunDataProcessingConformance(context.Background(), client)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
}
