// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type streamListFakeObservability struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	caps         *genv1.ClaimProducerObservabilityCapabilities
	streamEvents []*genv1.ClaimEvent
	streamErr    error
	listClaims   []*genv1.ClaimSummary
}

func (s *streamListFakeObservability) Capabilities(context.Context, *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return s.caps, nil
}

func (s *streamListFakeObservability) StreamClaim(_ *genv1.StreamClaimRequest, stream genv1.ClaimProducerObservability_StreamClaimServer) error {
	for _, ev := range s.streamEvents {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	return s.streamErr
}

func (s *streamListFakeObservability) ListClaims(context.Context, *genv1.ListClaimsRequest) (*genv1.ClaimList, error) {
	return &genv1.ClaimList{Claims: s.listClaims}, nil
}

func startStreamListFakeServer(t *testing.T, fake *streamListFakeObservability) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, fake)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	return lis.Addr().String()
}

func TestRunObservabilityCheck_StreamClaim_GarbageEventRejected(t *testing.T) {
	fake := &streamListFakeObservability{
		caps: &genv1.ClaimProducerObservabilityCapabilities{SupportsClaimStream: true},
		streamEvents: []*genv1.ClaimEvent{
			{EventId: ""},
		},
	}
	addr := startStreamListFakeServer(t, fake)
	err := RunObservabilityCheck(context.Background(), ObservabilityCheckOpts{Endpoint: addr}, nil)
	if err == nil {
		t.Fatal("expected rejection of a StreamClaim event with an empty event_id, got nil error")
	}
}

func TestRunObservabilityCheck_StreamClaim_MidStreamErrorRejected(t *testing.T) {
	fake := &streamListFakeObservability{
		caps:      &genv1.ClaimProducerObservabilityCapabilities{SupportsClaimStream: true},
		streamErr: errors.New("boom"),
	}
	addr := startStreamListFakeServer(t, fake)
	err := RunObservabilityCheck(context.Background(), ObservabilityCheckOpts{Endpoint: addr}, nil)
	if err == nil {
		t.Fatal("expected a non-EOF StreamClaim error to fail the check, got nil error")
	}
}

func TestRunObservabilityCheck_StreamClaim_CleanEmptyStreamAccepted(t *testing.T) {
	fake := &streamListFakeObservability{
		caps: &genv1.ClaimProducerObservabilityCapabilities{SupportsClaimStream: true},
	}
	addr := startStreamListFakeServer(t, fake)
	err := RunObservabilityCheck(context.Background(), ObservabilityCheckOpts{Endpoint: addr}, nil)
	if err != nil {
		t.Fatalf("expected a clean empty stream (io.EOF) for a missing claim to pass, got: %v", err)
	}
}

func TestRunObservabilityCheck_ListClaims_ExceedsLimitRejected(t *testing.T) {
	fake := &streamListFakeObservability{
		caps: &genv1.ClaimProducerObservabilityCapabilities{SupportsListClaims: true},
		listClaims: []*genv1.ClaimSummary{
			{ClaimId: "a"},
			{ClaimId: "b"},
		},
	}
	addr := startStreamListFakeServer(t, fake)
	err := RunObservabilityCheck(context.Background(), ObservabilityCheckOpts{Endpoint: addr}, nil)
	if err == nil {
		t.Fatal("expected rejection of a ListClaims response exceeding the requested limit=1, got nil error")
	}
}

func TestRunObservabilityCheck_ListClaims_WithinLimitAccepted(t *testing.T) {
	fake := &streamListFakeObservability{
		caps: &genv1.ClaimProducerObservabilityCapabilities{SupportsListClaims: true},
		listClaims: []*genv1.ClaimSummary{
			{ClaimId: "a"},
		},
	}
	addr := startStreamListFakeServer(t, fake)
	err := RunObservabilityCheck(context.Background(), ObservabilityCheckOpts{Endpoint: addr}, nil)
	if err != nil {
		t.Fatalf("expected a within-limit ListClaims response to pass, got: %v", err)
	}
}
