// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	dataprocessing "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/dataprocessing"
	"github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/lifecycle"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const gracefulStopBudget = 5 * time.Second

type Config struct {
	Substrate            stubstore.Config
	EnableLifecycle      bool
	EnableDataProcessing bool
}

func Run(ctx context.Context, cfg Config, grpcLis, httpLis net.Listener) error {
	st := stubstore.New(cfg.Substrate)
	return RunWithStore(ctx, cfg, st, grpcLis, httpLis)
}

func RunWithStore(ctx context.Context, cfg Config, st *stubstore.Store, grpcLis, httpLis net.Listener) error {
	srv := &Server{Store: st, EnableDataProcessing: cfg.EnableDataProcessing}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(grpcSrv, srv)
	if cfg.EnableLifecycle {
		genv1.RegisterLifecycleSubscriberServer(grpcSrv, lifecycle.NewServer())
	}
	if cfg.EnableDataProcessing {
		srv.DataProcessing = dataprocessing.New()
		genv1.RegisterDataProcessingServer(grpcSrv, srv.DataProcessing)
	}
	RegisterObservability(grpcSrv)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("stub store: grpc serve", "error", err.Error())
		}
	}()
	mux := http.NewServeMux()
	bridge.Mount(mux, srv)
	if cfg.EnableLifecycle {
		bridge.MountLifecycle(mux, lifecycle.NewServer())
	}
	httpSrv := &http.Server{Handler: mux}
	go func() {
		if err := httpSrv.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("stub store: http serve", "error", err.Error())
		}
	}()
	<-ctx.Done()
	stopTimer := time.AfterFunc(gracefulStopBudget, grpcSrv.Stop)
	grpcSrv.GracefulStop()
	stopTimer.Stop()
	_ = httpSrv.Close()
	return nil
}

type Server struct {
	genv1.UnimplementedClaimProducerServer
	Store                *stubstore.Store
	DataProcessing       *dataprocessing.Server
	EnableDataProcessing bool
}

func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.Store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsAllowed))
	for _, ws := range c.WriteSemanticsAllowed {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	protocols := []string{"claim_producer"}
	if s.EnableDataProcessing {
		protocols = append(protocols, "data_processing")
	}
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed:  out,
		SupportsSplitScope:     true,
		SupportsScopesConflict: true,
		Protocols:              protocols,
		DeclaredErrorClasses:   c.DeclaredErrorClasses,
	}, nil
}

func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, fmt.Errorf("stub.Open: intent must be \"r\" or \"rw\", got %q", intent)
	}
	outcome, err := s.Store.Open(ctx, req.GetClaimId(), req.GetSelector())
	if err != nil {
		return nil, err
	}
	if !outcome.Available {
		return &genv1.OpenResponse{
			Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{
				ErrorClass: outcome.UnavailableClass,
			}},
		}, nil
	}
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
			Address:                outcome.Result.Address,
			Payload:                outcome.Result.Payload,
			ClaimScope:             outcome.Result.ClaimScope,
			RealizedWriteSemantics: bridge.WriteSemanticsToProto(string(outcome.Result.RealizedWriteSemantics)),
		}},
	}, nil
}

func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	versionID, metadata := s.Store.CommitResponseFields()
	return &genv1.CommitResponse{
		VersionId:        versionID,
		ProducerMetadata: metadata,
	}, nil
}

func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.Store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}

func (s *Server) SplitScope(ctx context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	if s.DataProcessing != nil {
		return s.DataProcessing.SplitScope(ctx, req)
	}
	var probe struct {
		PartitionKeys []string          `json:"partition_keys"`
		List          []json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(req.GetPartitionRequest(), &probe); err != nil {
		return nil, fmt.Errorf("stub.SplitScope: decode partition_request: %w", err)
	}
	if probe.List != nil {
		return splitScopeList(probe.List)
	}
	if len(probe.PartitionKeys) == 0 {
		return nil, fmt.Errorf("stub.SplitScope: partition_request.partition_keys must be non-empty")
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(probe.PartitionKeys))
	for _, key := range probe.PartitionKeys {
		scope, _ := json.Marshal(map[string]string{"partition_key": key})
		out = append(out, &genv1.SubScopeDescriptor{
			ClaimScopeData: scope,
			PartitionKey:   key,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

func splitScopeList(elements []json.RawMessage) (*genv1.SplitScopeResponse, error) {
	out := make([]*genv1.SubScopeDescriptor, 0, len(elements))
	for i, raw := range elements {
		var el struct {
			Key     string          `json:"key"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &el); err != nil {
			return nil, fmt.Errorf("stub.SplitScope: decode list[%d]: %w", i, err)
		}
		if el.Key == "" {
			return nil, fmt.Errorf("stub.SplitScope: list[%d].key must be non-empty", i)
		}
		scope, _ := json.Marshal(map[string]string{"partition_key": el.Key})
		out = append(out, &genv1.SubScopeDescriptor{
			ClaimScopeData: scope,
			PartitionKey:   el.Key,
			Payload:        []byte(el.Payload),
			Address:        []byte{},
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

func (s *Server) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	return &genv1.ScopesConflictResponse{Conflicts: bytes.Equal(req.GetClaimScopeA(), req.GetClaimScopeB())}, nil
}
