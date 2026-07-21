// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestRun_FusedClaimProducer_OpenCommitRoundTripOnSharedGRPCServer(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)

	client := genv1.NewClaimProducerClient(dialFusedServer(t, dsn, true))

	openResp, err := client.Open(context.Background(), &genv1.OpenRequest{
		ClaimId:  "fused-cp-round-trip",
		Selector: "fused_cp_probe",
		Intent:   "r",
	})
	if err != nil {
		t.Fatalf("Open over the shared grpc.Server started by Run(EnableExecutor:true): %v", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		t.Fatalf("expected Acquired result, got %T", openResp.GetResult())
	}

	if _, err := client.Commit(context.Background(), &genv1.CommitRequest{
		ClaimId:    "fused-cp-round-trip",
		ClaimScope: acquired.GetClaimScope(),
		Address:    acquired.GetAddress(),
	}); err != nil {
		t.Fatalf("Commit over the shared grpc.Server: %v", err)
	}
}

func TestRun_FusedClaimProducer_CapabilitiesOnSharedGRPCServer(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)

	client := genv1.NewClaimProducerClient(dialFusedServer(t, dsn, true))

	resp, err := client.Capabilities(context.Background(), &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities over the shared grpc.Server started by Run(EnableExecutor:true): %v", err)
	}
	if len(resp.GetWriteSemanticsAllowed()) == 0 {
		t.Fatal("Capabilities.write_semantics_allowed must be non-empty")
	}
	if !resp.GetSupportsSplitScope() {
		t.Fatal("Capabilities.supports_split_scope must be true for the postgres store")
	}
	found := false
	for _, p := range resp.GetProtocols() {
		if p == "executor" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Capabilities.protocols = %v, want it to include %q since Run was started with EnableExecutor:true", resp.GetProtocols(), "executor")
	}
}
