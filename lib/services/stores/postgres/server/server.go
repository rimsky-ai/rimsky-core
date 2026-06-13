// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package server adapts the store-internal postgres Store to the
// rimsky ClaimProducer + LifecycleSubscriber gRPC + HTTP+JSON bridge.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/lifecycle"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the store's pgxpool when ctx is cancelled.
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config for the postgres store-service.
type Config struct {
	Connection     string
	WriteSemantics claimproducer.WriteSemantics
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

// producerDeclaredErrorClasses is the error-class vocabulary the store
// names on the ClaimProducer surface: the Unavailable.error_class arm
// of Open (pg/claim_unavailable) and the google.rpc.ErrorInfo Reason
// stamped on faulted producer verbs by classedStatus (pg/swap_failed).
// Advertised on CapabilitiesResponse.declared_error_classes so the
// template validator's `error_types:` range-check accepts these keys.
// The executor-side vocabulary (declaredErrorClasses in executor.go)
// is the separate ExecutorObservability surface.
func producerDeclaredErrorClasses() []string {
	return []string{
		pgsstore.ClaimUnavailableClass,
		pgsstore.SwapFailedClass,
	}
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsAllowed))
	for _, ws := range c.WriteSemanticsAllowed {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: out,
		DeclaredErrorClasses:  producerDeclaredErrorClasses(),
	}, nil
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
		// Carry the producer-declared acquisition-failure class (e.g.
		// pg/claim_unavailable) on the Unavailable arm so rimsky keys the
		// operator's `error_types:` chain on it. Empty when the store named
		// no class — preserving the synthetic `acquire/unavailable` routing.
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

// Commit delegates. A store-side failure carrying a rimsky error_class
// (e.g. the atomic-staging `pg/swap_failed` collision) is translated into
// a gRPC status with a google.rpc.ErrorInfo detail so rimsky's
// claim-producer client recovers the class and routes it through the
// holder's `error_types:` chain — giving the declared `pg/swap_failed`
// class a real signal at the subscriber surface.
func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.ReleaseResponse{}, nil
}

// classedStatus maps a store-side error into a gRPC status. When the
// error carries a rimsky error_class (a *store.ClassedError, e.g. the
// `pg/swap_failed` atomic-staging collision), the class is stamped into a
// google.rpc.ErrorInfo detail (Reason = class) — the exact shape
// `lib/runtime/peer.extractErrorClass` decodes — so the supervisor routes
// the producer-verb fault through the operator's `error_types:` chain.
// An unclassed error passes through as a bare Internal status.
func classedStatus(err error) error {
	if err == nil {
		return nil
	}
	var ce *pgsstore.ClassedError
	if !errors.As(err, &ce) || ce.Class == "" {
		return err
	}
	st := status.New(codes.Internal, ce.Error())
	withInfo, derr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: ce.Class,
		Domain: "rimsky.store-postgres",
	})
	if derr != nil {
		return st.Err()
	}
	return withInfo.Err()
}
