// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	stubserver "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
)

type commitFailingClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (commitFailingClaimProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func (commitFailingClaimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	addr := []byte(req.GetSelector())
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addr,
				ClaimScope:             addr,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			},
		},
	}, nil
}

func (commitFailingClaimProducer) Commit(context.Context, *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return nil, status.Error(codes.Internal, "conformance_claimproducer_cli_test: synthetic Commit failure")
}

func (commitFailingClaimProducer) Abandon(context.Context, *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (commitFailingClaimProducer) Release(context.Context, *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

func startCommitFailingClaimProducer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, commitFailingClaimProducer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestRunConformanceClaimProducer_ExitsNonZeroWhenACheckFails(t *testing.T) {
	endpoint := startCommitFailingClaimProducer(t)

	code := runConformanceClaimProducer([]string{"--endpoint", "grpc://" + endpoint})
	if code == 0 {
		t.Fatal("rimsky conformance claim-producer must exit non-zero when a check fails (Commit is wired to always error here), got exit code 0")
	}
}

func TestRunConformanceClaimProducer_ExitsZeroWhenAllChecksPass(t *testing.T) {
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}
	cfg := stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	}
	st := stubstore.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = stubserver.RunWithStore(ctx, stubserver.Config{Substrate: cfg}, st, grpcLis, httpLis)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	code := runConformanceClaimProducer([]string{"--endpoint", "grpc://" + grpcLis.Addr().String()})
	if code != 0 {
		t.Fatalf("rimsky conformance claim-producer against a fully-honest stub store must exit 0, got %d", code)
	}
}
