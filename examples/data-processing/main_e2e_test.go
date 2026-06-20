// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func TestE2E_ExampleDataProcessingProtocolSurfaces(t *testing.T) {
	t.Parallel()

	dp, endpoint, stop := startExampleDataProcessing(t)
	defer stop()

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := peer.DialDataProcessing(dialCtx, "example", endpoint, peer.TLSModeOff)
	if err != nil {
		t.Fatalf("peer.DialDataProcessing(%q): %v", endpoint, err)
	}
	defer client.Close()

	var _ clientiface.DataProcessingClient = client

	t.Run("Capabilities_advertises_non_empty_set", func(t *testing.T) {
		exerciseCapabilitiesLeg(t, endpoint)
	})

	t.Run("BeginCommit_lands_and_surfaces_in_ListVersions", func(t *testing.T) {
		exerciseBeginCommitListLeg(t, dp, client)
	})

	t.Run("BeginAbandon_lands_and_does_NOT_surface_in_ListVersions", func(t *testing.T) {
		exerciseBeginAbandonLeg(t, dp, client)
	})
}

func exerciseCapabilitiesLeg(t *testing.T, endpoint string) {
	t.Helper()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial example for Capabilities: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps, err := genv1.NewDataProcessingClient(conn).Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.GetDataShapes()) == 0 && len(caps.GetMaterializations()) == 0 &&
		len(caps.GetPartitionKinds()) == 0 && len(caps.GetAggregators()) == 0 {
		t.Fatal("Capabilities advertised an empty capability set — a producer that materializes nothing is silently non-functional")
	}
}

func exerciseBeginCommitListLeg(t *testing.T, dp *DataProcessing, client *peer.DataProcessingClient) {
	t.Helper()
	const claimID = "claim-success"
	const subScope = "tenant/a/2026-06"

	beforeCommit := dp.CommitCount()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	beginOut, err := client.BeginCandidate(ctx, clientiface.BeginCandidateInput{
		ProducerName:       "example",
		ClaimHandleID:      claimID,
		SubScopeDescriptor: []byte(subScope),
		IdempotencyKey:     "run-success-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(beginOut.CandidateHandle) == 0 {
		t.Fatal("BeginCandidate returned an empty candidate_handle — rimsky persists this on " +
			"col:rimsky_claim_handles.producer_candidate_handle and the leaf dispatch reads it " +
			"back on ClaimProducerHandle.candidate_handle; an empty handle would make the leaf dispatch " +
			"with no producer cursor, the falsifier for \"BeginCandidate is never called on a " +
			"fan-out partition\" in canned-handler form")
	}

	commitOut, err := client.CommitCandidate(ctx, clientiface.CommitCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	})
	if err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if afterCommit := dp.CommitCount(); afterCommit != beforeCommit+1 {
		t.Fatalf("CommitCount did not grow against the live handler: before=%d after=%d — "+
			"the rimsky-side client returned OK but the producer's effect was canned "+
			"(falsifier: \"CommitCandidate is called but the producer's effect is canned\")",
			beforeCommit, afterCommit)
	}
	if len(commitOut.CandidateMetadata) == 0 {
		t.Fatal("CommitCandidate returned empty candidate_metadata — the producer surfaces " +
			"the per-version metadata via the parent's writeback; empty metadata would mean " +
			"the rimsky-side commit lands but the producer's metadata is canned")
	}
	var meta struct {
		VersionID string `json:"version_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(commitOut.CandidateMetadata, &meta); err != nil {
		t.Fatalf("decode CommitCandidate metadata %q: %v", string(commitOut.CandidateMetadata), err)
	}
	if meta.Status != "committed" {
		t.Fatalf("CommitCandidate metadata.status=%q, want %q (the example's commit-time marker)",
			meta.Status, "committed")
	}
	if meta.VersionID == "" {
		t.Fatal("CommitCandidate metadata.version_id is empty — the example declares it at commit " +
			"time, so an empty value would mean the producer's effect is canned")
	}

	lvOut, err := client.ListVersions(ctx, clientiface.ListVersionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
	})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if !versionListed(lvOut.Versions, meta.VersionID) {
		t.Fatalf("declared version_id %q is missing from ListVersions response %+v — "+
			"falsifier: \"a declared version doesn't appear in ListVersions\"",
			meta.VersionID, lvOut.Versions)
	}

	lpOut, err := client.ListPartitions(ctx, clientiface.ListPartitionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
		VersionID:     meta.VersionID,
	})
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(lpOut.Partitions) == 0 {
		t.Fatal("ListPartitions returned an empty partition list for a version the example just " +
			"committed against a non-empty sub-scope")
	}
	if !partitionKeyed(lpOut.Partitions, subScope) {
		t.Fatalf("ListPartitions returned %+v, none of which carry partition_key=%q — the example "+
			"seeds the partition from BeginCandidate's sub_scope_descriptor, so a missing key "+
			"means the partition list is canned",
			lpOut.Partitions, subScope)
	}

	gsOut, err := client.GetVersionSchema(ctx, clientiface.GetVersionSchemaInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
		VersionID:     meta.VersionID,
	})
	if err != nil {
		t.Fatalf("GetVersionSchema: %v", err)
	}
	if len(gsOut.Schema) == 0 {
		t.Fatal("GetVersionSchema returned an empty schema for a version the example just committed " +
			"— the example seeds a JSON Schema at commit time so an empty response means the " +
			"producer's effect is canned")
	}
	if !json.Valid(gsOut.Schema) {
		t.Fatalf("GetVersionSchema returned non-JSON schema bytes %q — the example seeds a JSON "+
			"Schema at commit, so non-JSON bytes mean the producer is returning a stale or "+
			"canned blob", string(gsOut.Schema))
	}
}

func exerciseBeginAbandonLeg(t *testing.T, dp *DataProcessing, client *peer.DataProcessingClient) {
	t.Helper()
	const claimID = "claim-abandon"
	const subScope = "tenant/b/2026-06"

	beforeAbandon := dp.AbandonCount()
	beforeCommit := dp.CommitCount()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	beginOut, err := client.BeginCandidate(ctx, clientiface.BeginCandidateInput{
		ProducerName:       "example",
		ClaimHandleID:      claimID,
		SubScopeDescriptor: []byte(subScope),
		IdempotencyKey:     "run-abandon-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(beginOut.CandidateHandle) == 0 {
		t.Fatal("BeginCandidate returned an empty candidate_handle on the abandon leg")
	}

	if err := client.AbandonCandidate(ctx, clientiface.AbandonCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	}); err != nil {
		t.Fatalf("AbandonCandidate: %v", err)
	}
	if afterAbandon := dp.AbandonCount(); afterAbandon != beforeAbandon+1 {
		t.Fatalf("AbandonCount did not grow against the live handler: before=%d after=%d — "+
			"falsifier: \"AbandonCandidate is skipped on leaf failure\" fails when the verb "+
			"reaches the wire but the producer's effect is canned",
			beforeAbandon, afterAbandon)
	}
	if afterCommit := dp.CommitCount(); afterCommit != beforeCommit {
		t.Fatalf("CommitCount changed across an Abandon-only leg: before=%d after=%d — "+
			"an Abandon must NOT silently promote the candidate into a committed version",
			beforeCommit, afterCommit)
	}

	lvOut, err := client.ListVersions(ctx, clientiface.ListVersionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
	})
	if err != nil {
		t.Fatalf("ListVersions(%q) after abandon: %v", claimID, err)
	}
	if len(lvOut.Versions) != 0 {
		t.Fatalf("ListVersions(%q) returned %+v after AbandonCandidate, want empty — an abandoned "+
			"candidate must NOT surface in the version history",
			claimID, lvOut.Versions)
	}

	if err := client.AbandonCandidate(ctx, clientiface.AbandonCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	}); err != nil {
		t.Fatalf("AbandonCandidate (repeat): %v — the producer must tolerate a retried "+
			"terminal verb on an unknown handle", err)
	}
}

func startExampleDataProcessing(t *testing.T) (*DataProcessing, string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	dp := newDataProcessing()
	genv1.RegisterDataProcessingServer(srv, dp)
	go func() { _ = srv.Serve(lis) }()

	endpoint := lis.Addr().String()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			stop := func() { srv.Stop() }
			return dp, endpoint, stop
		}
		time.Sleep(25 * time.Millisecond)
	}
	srv.Stop()
	t.Fatalf("in-process example DataProcessing did not become dialable at %s within 10s", endpoint)
	return nil, "", func() {}
}

func versionListed(got []clientiface.DataProcessingVersion, want string) bool {
	for _, v := range got {
		if v.VersionID == want {
			return true
		}
	}
	return false
}

func partitionKeyed(got []clientiface.DataProcessingPartition, want string) bool {
	for _, p := range got {
		if p.PartitionKey == want {
			return true
		}
	}
	return false
}
