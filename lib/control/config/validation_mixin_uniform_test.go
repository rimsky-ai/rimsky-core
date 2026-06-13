// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// validation_mixin_uniform_test.go — executable acceptance proof for
// STORY-validation-mixin-uniform: a conformance-style test that stands
// up one real gRPC stub peer per kind (claim producer, executor,
// publisher), each advertising the validation mix-in with the same
// supported roles on its own capability surface, runs the real
// registry dial (DialPublisherAndValidationRegistries), and asserts
// the handshake-learned role sets are identical and non-empty across
// all three kinds. The falsifier this argues against: an executor or
// publisher advertising the mix-in whose supported-roles list is still
// treated as empty — dialed but never used.

package config

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// mixinRoles is the shared validation_supported_roles list every stub
// peer advertises. Two entries so an accidental truncation to a
// single-element list is also caught.
var mixinRoles = []string{"claim_producer", "executor"}

// stubValidationService answers Validate on every stub peer so each
// stub genuinely serves the validation mix-in, not just its
// capability surface.
type stubValidationService struct {
	genv1.UnimplementedValidationServer
}

func (stubValidationService) Validate(context.Context, *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	return &genv1.ValidateResponse{}, nil
}

// stubClaimProducerService implements the ClaimProducer Capabilities
// handshake the store-kind dial path runs (peer.Dial), advertising the
// validation mix-in with mixinRoles. WriteSemanticsAllowed must be
// non-empty and known or the handshake itself rejects the peer.
type stubClaimProducerService struct {
	genv1.UnimplementedClaimProducerServer
}

func (stubClaimProducerService) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed:    []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:                []string{"claim_producer", "validation"},
		ValidationSupportedRoles: mixinRoles,
	}, nil
}

// stubExecutorObservabilityService implements the
// ExecutorObservability Capabilities handshake the executor-kind dial
// path runs, advertising the validation mix-in with mixinRoles.
type stubExecutorObservabilityService struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (stubExecutorObservabilityService) Capabilities(context.Context, *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ValidationSupportedRoles: mixinRoles,
	}, nil
}

// stubPublisherService implements the Publisher Capabilities handshake
// the publisher-kind dial path runs, advertising the validation mix-in
// with mixinRoles.
type stubPublisherService struct {
	genv1.UnimplementedPublisherServer
}

func (stubPublisherService) Capabilities(context.Context, *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		Protocols:                []string{"publisher", "validation"},
		ValidationSupportedRoles: mixinRoles,
	}, nil
}

// startStubPeer serves the given register functions on a loopback
// listener and returns the dialable endpoint. The server stops at test
// cleanup.
func startStubPeer(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// TestValidationMixinUniformAcrossPeerKinds is the executable proof
// for STORY-validation-mixin-uniform.
func TestValidationMixinUniformAcrossPeerKinds(t *testing.T) {
	cpEndpoint := startStubPeer(t, func(s *grpc.Server) {
		genv1.RegisterClaimProducerServer(s, stubClaimProducerService{})
		genv1.RegisterValidationServer(s, stubValidationService{})
	})
	execEndpoint := startStubPeer(t, func(s *grpc.Server) {
		genv1.RegisterExecutorObservabilityServer(s, stubExecutorObservabilityService{})
		genv1.RegisterValidationServer(s, stubValidationService{})
	})
	pubEndpoint := startStubPeer(t, func(s *grpc.Server) {
		genv1.RegisterPublisherServer(s, stubPublisherService{})
		genv1.RegisterValidationServer(s, stubValidationService{})
	})

	stores := RemoteStoresConfig{Stores: map[string]StoreEntry{
		"producer-peer": {
			Endpoint:  "grpc://" + cpEndpoint,
			Protocols: []string{ProtocolClaimProducer, claimproducer.ProtocolValidation},
		},
	}}
	execs := ExecutorsConfig{Executors: map[string]ExecutorEntry{
		"executor-peer": {
			Transport: "grpc",
			Endpoint:  "grpc://" + execEndpoint,
			Protocols: []string{ProtocolExecutor, claimproducer.ProtocolValidation},
		},
	}}
	publishers := RemotePublishersConfig{Publishers: map[string]PublisherEntry{
		"publisher-peer": {
			Endpoint:  "grpc://" + pubEndpoint,
			Protocols: []string{ProtocolPublisher, claimproducer.ProtocolValidation},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, validators, _, closers, err := DialPublisherAndValidationRegistries(ctx, stores, execs, publishers)
	if err != nil {
		t.Fatalf("DialPublisherAndValidationRegistries: %v", err)
	}
	defer func() {
		for _, c := range closers {
			c()
		}
	}()

	want := append([]string(nil), mixinRoles...)
	sort.Strings(want)
	for _, peerName := range []string{"producer-peer", "executor-peer", "publisher-peer"} {
		client, ok := validators.Get(peerName)
		if !ok {
			t.Fatalf("validation registry missing peer %q", peerName)
		}
		got := append([]string(nil), client.SupportedRoles()...)
		if len(got) == 0 {
			t.Fatalf("peer %q: handshake-learned validation roles are empty — the mix-in was dialed but would never be used", peerName)
		}
		sort.Strings(got)
		if len(got) != len(want) {
			t.Fatalf("peer %q: roles = %v, want %v", peerName, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("peer %q: roles = %v, want %v", peerName, got, want)
			}
		}
	}
}
