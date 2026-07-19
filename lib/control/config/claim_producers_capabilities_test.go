// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type syncOnlyCapabilitiesProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (syncOnlyCapabilitiesProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func startPlaintextSyncOnlyClaimProducer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, syncOnlyCapabilitiesProducer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%s): %v", lis.Addr(), err)
	}
	return "localhost:" + port
}

func TestDialRemoteClaimProducers_OperatorDeclaredSupersetOfProducerAdvertised_FailsFast(t *testing.T) {
	endpoint := startPlaintextSyncOnlyClaimProducer(t)

	cfg := RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
		"items-store": {
			Endpoint: endpoint,
			TLS:      peer.TLSModeOff,
			Capabilities: claimproducer.Capabilities{
				WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsStagedAsync},
			},
		},
	}}

	reg, err := dialRemoteClaimProducers(context.Background(), cfg, nil, nil)
	if err == nil {
		if reg != nil {
			reg.Close()
		}
		t.Fatalf("dialRemoteClaimProducers: operator declared write_semantics_allowed=[async] but producer only advertises [sync]; want a fail-fast subset-violation error, got nil")
	}
	if !strings.Contains(err.Error(), "capabilities mismatch") {
		t.Fatalf("dialRemoteClaimProducers error = %q; want it to name the capabilities-mismatch cause", err.Error())
	}
	if reg != nil {
		t.Fatalf("dialRemoteClaimProducers: expected nil registry on subset-violation failure, got %+v", reg)
	}
}

func TestDialRemoteClaimProducers_OperatorDeclaredSubsetOfProducerAdvertised_Succeeds(t *testing.T) {
	endpoint := startPlaintextSyncOnlyClaimProducer(t)

	cfg := RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
		"items-store": {
			Endpoint: endpoint,
			TLS:      peer.TLSModeOff,
			Capabilities: claimproducer.Capabilities{
				WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
			},
		},
	}}

	reg, err := dialRemoteClaimProducers(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("dialRemoteClaimProducers: operator-declared [sync] is a valid subset of producer-advertised [sync]; want success, got %v", err)
	}
	if reg == nil {
		t.Fatalf("dialRemoteClaimProducers: expected non-nil registry on success")
	}
	reg.Close()
}
