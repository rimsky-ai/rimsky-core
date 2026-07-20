// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type retentionProbeFakeState struct {
	mu       sync.Mutex
	states   map[string]genv1.ClaimState
	evicting bool
}

func newRetentionProbeFakeState(evicting bool) *retentionProbeFakeState {
	return &retentionProbeFakeState{
		states:   make(map[string]genv1.ClaimState),
		evicting: evicting,
	}
}

func (s *retentionProbeFakeState) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evicting = true
}

type retentionProbeFakeClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
	state *retentionProbeFakeState
}

func (s *retentionProbeFakeClaimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.states[req.GetClaimId()] = genv1.ClaimState_OPEN
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                []byte(req.GetClaimId()),
		ClaimScope:             []byte(req.GetClaimId()),
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
	}}}, nil
}

func (s *retentionProbeFakeClaimProducer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.states[req.GetClaimId()] = genv1.ClaimState_COMMITTED
	return &genv1.CommitResponse{}, nil
}

type retentionProbeFakeObservability struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	state *retentionProbeFakeState
}

func (s *retentionProbeFakeObservability) Capabilities(context.Context, *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{SupportsClaimGet: true}, nil
}

func (s *retentionProbeFakeObservability) GetClaim(_ context.Context, req *genv1.GetClaimRequest) (*genv1.ClaimDetail, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	state, ok := s.state.states[req.GetClaimId()]
	if !ok || s.state.evicting {
		return &genv1.ClaimDetail{ClaimId: req.GetClaimId(), State: genv1.ClaimState_UNKNOWN}, nil
	}
	return &genv1.ClaimDetail{ClaimId: req.GetClaimId(), State: state}, nil
}

func startRetentionProbeFakeServer(t *testing.T, state *retentionProbeFakeState) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(grpcSrv, &retentionProbeFakeClaimProducer{state: state})
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, &retentionProbeFakeObservability{state: state})
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRunRetentionProbe_DrivesClaimAndDetectsHonestEviction(t *testing.T) {
	fake := newRetentionProbeFakeState(false)
	conn := startRetentionProbeFakeServer(t, fake)
	claimClient := genv1.NewClaimProducerClient(conn)
	obsClient := genv1.NewClaimProducerObservabilityClient(conn)

	waitAndEvict := func(context.Context, time.Duration) error {
		fake.evict()
		return nil
	}

	err := runRetentionProbe(context.Background(), claimClient, obsClient, 1, func(string, ...any) {}, waitAndEvict)
	if err != nil {
		t.Fatalf("runRetentionProbe: expected PASS for a producer that evicts a driven claim after its retention window, got: %v", err)
	}
}

func TestRunRetentionProbe_NoRetentionAtAllFails(t *testing.T) {
	fake := newRetentionProbeFakeState(false)
	conn := startRetentionProbeFakeServer(t, fake)
	claimClient := genv1.NewClaimProducerClient(conn)
	obsClient := genv1.NewClaimProducerObservabilityClient(conn)

	noWait := func(context.Context, time.Duration) error { return nil }

	err := runRetentionProbe(context.Background(), claimClient, obsClient, 1, func(string, ...any) {}, noWait)
	if err == nil {
		t.Fatalf("runRetentionProbe: expected non-nil error for a producer with no retention at all (GetClaim never returns UNKNOWN after the wait), got PASS")
	}
}

func TestRunRetentionProbe_ClaimInvisibleImmediatelyAfterOpenFails(t *testing.T) {
	fake := newRetentionProbeFakeState(true)
	conn := startRetentionProbeFakeServer(t, fake)
	claimClient := genv1.NewClaimProducerClient(conn)
	obsClient := genv1.NewClaimProducerObservabilityClient(conn)

	noWait := func(context.Context, time.Duration) error { return nil }

	err := runRetentionProbe(context.Background(), claimClient, obsClient, 1, func(string, ...any) {}, noWait)
	if err == nil {
		t.Fatalf("runRetentionProbe: expected non-nil error when a just-opened/committed claim is already UNKNOWN, got PASS")
	}
}
