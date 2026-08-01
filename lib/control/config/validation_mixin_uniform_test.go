// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

var mixinRoles = []string{"claim_producer", "executor"}

type stubValidationService struct {
	genv1.UnimplementedValidationServer
}

func (stubValidationService) Validate(context.Context, *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	return &genv1.ValidateResponse{}, nil
}

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

type stubExecutorObservabilityService struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (stubExecutorObservabilityService) Capabilities(context.Context, *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ValidationSupportedRoles: mixinRoles,
	}, nil
}

type stubPublisherService struct {
	genv1.UnimplementedPublisherServer
}

func (stubPublisherService) Capabilities(context.Context, *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		Protocols:                []string{"publisher", "validation"},
		ValidationSupportedRoles: mixinRoles,
	}, nil
}

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

// @story: validation-mixin-uniform
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

	producers := RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
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
	_, validators, _, closers, err := DialPublisherAndValidationRegistries(ctx, producers, execs, publishers, RemoteValidatorsConfig{}, RemoteDataProcessorsConfig{})
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
