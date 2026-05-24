// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package server adapts the store-internal stub Store to the rimsky
// ClaimProducer + LifecycleSubscriber gRPC + HTTP+JSON bridge.
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

	"github.com/fallguy/rimsky/stores/internal/bridge"
	dataprocessing "github.com/fallguy/rimsky/stores/stub/dataprocessing"
	"github.com/fallguy/rimsky/stores/stub/lifecycle"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the server when ctx is cancelled. Mirrors the
// postgres server's pattern.
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config.
type Config struct {
	Substrate stubstore.Config
	// EnableLifecycle, when true, registers the LifecycleSubscriber
	// service alongside ClaimProducer. The stub's lifecycle subscriber
	// is a no-op (returns nil from every method).
	EnableLifecycle bool
	// EnableDataProcessing, when true, registers the DataProcessing
	// gRPC service alongside ClaimProducer and flips Capabilities to
	// advertise `data_processing` in Protocols.
	//
	// SplitScope and ScopesConflict are always implemented on the
	// ClaimProducer surface (advertised via SupportsSplitScope /
	// SupportsScopesConflict in Capabilities) — the stub is the
	// M / N6 / N7 / O1 self-test target and both are cheap.
	EnableDataProcessing bool
}

// Run starts the gRPC + HTTP listeners and serves until ctx is
// cancelled. Mirrors the filesystem signature; postgres adds an admin
// listener (5th arg) for items insertion, while filesystem and stub
// have no admin surface.
func Run(ctx context.Context, cfg Config, grpcLis, httpLis net.Listener) error {
	st := stubstore.New(cfg.Substrate)
	return RunWithStore(ctx, cfg, st, grpcLis, httpLis)
}

// RunWithStore is the testfixture-friendly entry point: callers pass an
// already-constructed *stubstore.Store so they can keep a handle for
// test assertions while the server's lifetime is bounded by ctx.
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

// Server implements genv1.ClaimProducerServer. SplitScope and
// ScopesConflict are implemented in-process on this surface; the
// stub-store advertises both via Capabilities so M / N6 / N7 / O1
// can drive the SplitScope fan-out path without a separate fixture.
type Server struct {
	genv1.UnimplementedClaimProducerServer
	Store          *stubstore.Store
	DataProcessing *dataprocessing.Server
	// EnableDataProcessing mirrors Config.EnableDataProcessing so
	// Capabilities flips the `data_processing` protocol advertisement
	// without reading DataProcessing == nil (which a test might
	// inject for the no-DP case).
	EnableDataProcessing bool
}

// Capabilities returns the store's advertised capability struct.
// SplitScope and ScopesConflict are always advertised on the
// stub-store wire so the M / N / O suites can pin against a single
// fixture without juggling per-test caps.
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
	}, nil
}

// Open delegates. Validates `intent` against the wire schema (only "r"
// or "rw") before dispatching, mirroring the HTTP bridge's gate so
// direct-gRPC callers can't bypass the check.
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
			Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
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

// Commit delegates.
func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.Store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}

// SplitScope partitions the parent claim's scope into N sub-scopes
// per partition_request. The stub-store delegates to the
// DataProcessing impl when present (so both surfaces share the same
// decoder); falls back to the standalone decoder otherwise. Per spec
// §Fan-out template DSL and concept:fan-out.
func (s *Server) SplitScope(ctx context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	if s.DataProcessing != nil {
		return s.DataProcessing.SplitScope(ctx, req)
	}
	var decoded struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(req.GetPartitionRequest(), &decoded); err != nil {
		return nil, fmt.Errorf("stub.SplitScope: decode partition_request: %w", err)
	}
	if len(decoded.PartitionKeys) == 0 {
		return nil, fmt.Errorf("stub.SplitScope: partition_request.partition_keys must be non-empty")
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(decoded.PartitionKeys))
	for _, key := range decoded.PartitionKeys {
		scope, _ := json.Marshal(map[string]string{"partition_key": key})
		out = append(out, &genv1.SubScopeDescriptor{
			ClaimScopeData: scope,
			PartitionKey:   key,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

// ScopesConflict returns true iff a and b are byte-equal. The
// stub-store has no producer-specific overlap semantics, so it
// honors the trivial byte-equal default while still advertising
// SupportsScopesConflict so test suites can exercise the wire
// path. Per @blessed-invariant 4b's fallback semantics.
func (s *Server) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	return &genv1.ScopesConflictResponse{Conflicts: bytes.Equal(req.GetClaimScopeA(), req.GetClaimScopeB())}, nil
}
