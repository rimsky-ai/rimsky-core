// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package server adapts the store-internal postgres Store to the
// rimsky ClaimProducer + LifecycleSubscriber gRPC + HTTP+JSON bridge.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	corestore "github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/stores/internal/bridge"
	"github.com/fallguyconsulting/rimsky/stores/postgres/lifecycle"
	pgsstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the store's pgxpool when ctx is cancelled.
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config for the postgres store-service.
type Config struct {
	Connection     string
	WriteSemantics corestore.WriteSemantics
	PickPolicies   map[string]*pgsstore.PickPolicy
	SweepInterval  time.Duration
	// HTTPBridgeURL is the externally-reachable HTTP base URL for
	// dashboard clients. Surfaced via ClaimProducerObservabilityCapabilities.
	HTTPBridgeURL string
	// EnableLifecycle, when true, registers the LifecycleSubscriber
	// service alongside ClaimProducer.
	EnableLifecycle bool
	// EnableExecutor, when true, registers the Executor service
	// alongside ClaimProducer for the verifier role. The executor
	// consumes an attributes `checks: [...]` DSL and runs read-only
	// aggregate SQL against the schema named by
	// {{claim.<alias>.address}}. Per spec
	// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
	// §Item 6.
	//
	// @concept: executor
	EnableExecutor bool
}

// Run starts the gRPC + HTTP + admin listeners and the store's
// internal sweep goroutine. Returns when ctx is cancelled.
func Run(ctx context.Context, cfg Config, grpcLis, httpLis, adminLis net.Listener) error {
	st, err := pgsstore.New(ctx, pgsstore.Config{
		Connection:     cfg.Connection,
		WriteSemantics: cfg.WriteSemantics,
		PickPolicies:   cfg.PickPolicies,
	})
	if err != nil {
		return err
	}

	srv := &Server{store: st}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(grpcSrv, srv)
	if cfg.EnableLifecycle {
		genv1.RegisterLifecycleSubscriberServer(grpcSrv, lifecycle.NewServer())
	}
	if cfg.EnableExecutor {
		genv1.RegisterExecutorServer(grpcSrv, NewExecutorServer(st))
		// 2026-05-23 signal-taxonomy Pass 6: surface the verifier
		// executor's hierarchical error vocabulary via the executor
		// observability handshake so operator templates'
		// `error_types:` keys can be range-checked at registration.
		genv1.RegisterExecutorObservabilityServer(grpcSrv, NewExecutorObservabilityServer())
	}
	obsSrv := srv.RegisterObservability(grpcSrv)
	obsSrv.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("postgres store: grpc serve", "error", err.Error())
		}
	}()

	httpMux := http.NewServeMux()
	bridge.Mount(httpMux, srv)
	if cfg.EnableLifecycle {
		bridge.MountLifecycle(httpMux, lifecycle.NewServer())
	}
	bridge.MountObservability(httpMux, obsSrv)
	httpSrv := &http.Server{Handler: httpMux}
	go func() {
		if err := httpSrv.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("postgres store: http serve", "error", err.Error())
		}
	}()

	var adminSrv *http.Server
	if adminLis != nil {
		adminSrv = &http.Server{Handler: st.AdminHandler()}
		go func() {
			if err := adminSrv.Serve(adminLis); err != nil && err != http.ErrServerClosed {
				slog.Warn("postgres store: admin serve", "error", err.Error())
			}
		}()
	}

	go st.RunSweep(ctx, cfg.SweepInterval)

	<-ctx.Done()
	stopTimer := time.AfterFunc(gracefulStopBudget, grpcSrv.Stop)
	grpcSrv.GracefulStop()
	stopTimer.Stop()
	_ = httpSrv.Close()
	if adminSrv != nil {
		_ = adminSrv.Close()
	}
	st.Close()
	return nil
}

// Server implements genv1.ClaimProducerServer.
type Server struct {
	genv1.UnimplementedClaimProducerServer
	store *pgsstore.Store
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsAllowed))
	for _, ws := range c.WriteSemanticsAllowed {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	return &genv1.CapabilitiesResponse{WriteSemanticsAllowed: out}, nil
}

// Open delegates. Validates `intent` against the wire schema (only
// "r" or "rw") before dispatching, mirroring the HTTP bridge's gate
// so direct-gRPC callers can't bypass the check.
func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, fmt.Errorf("postgres.Open: intent must be \"r\" or \"rw\", got %q", intent)
	}
	outcome, err := s.store.Open(ctx, req.GetClaimId(), req.GetSelector())
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
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}
