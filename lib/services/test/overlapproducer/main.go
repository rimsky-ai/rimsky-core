// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const defaultBind = "0.0.0.0:9400"

type overlapProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func overlapCaps() *genv1.CapabilitiesResponse {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
		},
		SupportsScopesConflict: true,
		SupportsSplitScope:     true,
	}
}

func (p *overlapProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return overlapCaps(), nil
}

func (p *overlapProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	addr, _ := json.Marshal(req.GetClaimId())
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addr,
				ClaimScope:             nil,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			},
		},
	}, nil
}

func (p *overlapProducer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *overlapProducer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *overlapProducer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

func (p *overlapProducer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	a := decodeSelector(req.GetClaimScopeA())
	b := decodeSelector(req.GetClaimScopeB())
	return &genv1.ScopesConflictResponse{Conflicts: prefixOverlap(a, b)}, nil
}

func (p *overlapProducer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	keys, err := decodePartitionKeys(req.GetPartitionRequest())
	if err != nil {
		return nil, fmt.Errorf("overlapproducer: SplitScope: malformed partition_request: %w", err)
	}
	subs := make([]*genv1.SubScopeDescriptor, 0, len(keys))
	for _, k := range keys {
		selector := "tenant/" + k
		scopeBytes, _ := json.Marshal(selector)
		subs = append(subs, &genv1.SubScopeDescriptor{
			ClaimScopeData: scopeBytes,
			PartitionKey:   k,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: subs}, nil
}

func prefixOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func decodeSelector(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func decodePartitionKeys(raw []byte) ([]string, error) {
	var req struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return req.PartitionKeys, nil
}

func main() {
	bind := os.Getenv("OVERLAP_PRODUCER_BIND")
	if bind == "" {
		bind = defaultBind
	}
	lis, err := net.Listen("tcp", bind)
	if err != nil {
		slog.Error("OVERLAPPRODUCER.GRPC.LISTENFAILED", "bind", bind, "error", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, &overlapProducer{})
	slog.Info("OVERLAPPRODUCER.GRPC.LISTENING", "bind", bind)
	if err := srv.Serve(lis); err != nil {
		slog.Error("OVERLAPPRODUCER.GRPC.SERVEFAILED", "error", err)
		os.Exit(1)
	}
}
