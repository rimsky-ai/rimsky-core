// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestBeginThenCommitCandidate_RoundTrips starts the example DataProcessing
// service in-process over gRPC and asserts the candidate lifecycle round-trips:
//
//   - Capabilities advertises a non-empty capability set;
//   - BeginCandidate returns a non-empty candidate_handle;
//   - CommitCandidate on that handle succeeds and returns non-empty metadata.
//
// This exercises the real fan-out leaf lifecycle a copied producer must
// implement (BeginCandidate at dispatch, CommitCandidate at leaf success).
func TestBeginThenCommitCandidate_RoundTrips(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterDataProcessingServer(srv, newDataProcessing())
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := genv1.NewDataProcessingClient(conn)

	caps, err := client.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(caps.GetDataShapes()) == 0 && len(caps.GetMaterializations()) == 0 &&
		len(caps.GetPartitionKinds()) == 0 && len(caps.GetAggregators()) == 0 {
		t.Fatal("Capabilities advertised an empty capability set")
	}

	begin, err := client.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      "claim-1",
		SubScopeDescriptor: []byte("tenant/a/2026-01"),
		IdempotencyKey:     "run-1",
	})
	if err != nil {
		t.Fatalf("begin candidate: %v", err)
	}
	handle := begin.GetCandidateHandle()
	if len(handle) == 0 {
		t.Fatal("BeginCandidate returned an empty candidate_handle")
	}

	commit, err := client.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: handle,
	})
	if err != nil {
		t.Fatalf("commit candidate: %v", err)
	}
	if len(commit.GetCandidateMetadata()) == 0 {
		t.Fatal("CommitCandidate returned empty candidate_metadata")
	}
}
