// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package server adapts the store-internal filesystem Store to the
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

	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/lifecycle"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the server when ctx is cancelled.
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config for the filesystem store-service.
type Config struct {
	Root          string
	PickPolicies  map[string]*fsstore.PickPolicy
	SweepInterval time.Duration
	// HTTPBridgeURL is the externally-reachable HTTP base URL for
	// dashboard clients. Surfaced through ClaimProducerObservabilityCapabilities.
	// Empty when not declared; the dashboard then falls back to the
	// dispatch endpoint and HTTP-only routes (claims/admin) won't work.
	HTTPBridgeURL string
	// EnableLifecycle, when true, registers the LifecycleSubscriber
	// service alongside ClaimProducer. Currently a no-op subscriber.
	EnableLifecycle bool
}

// Run starts the gRPC and HTTP+JSON listeners and serves until ctx is
// cancelled. cmd/main.go and testfixture/ both call this; main loads
// cfg from YAML, testfixture builds it programmatically. adminLis may
// be nil — when nil, the admin handler is not exposed.
func Run(ctx context.Context, cfg Config, grpcLis, httpLis, adminLis net.Listener) error {
	st, err := fsstore.New(fsstore.Config{
		Root:         cfg.Root,
		PickPolicies: cfg.PickPolicies,
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
	obsSrv := srv.RegisterObservability(grpcSrv, cfg.Root, cfg.PickPolicies)
	obsSrv.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("filesystem store: grpc serve", "error", err.Error())
		}
	}()

	mux := http.NewServeMux()
	bridge.Mount(mux, srv)
	if cfg.EnableLifecycle {
		bridge.MountLifecycle(mux, lifecycle.NewServer())
	}
	bridge.MountObservability(mux, obsSrv)
	httpSrv := &http.Server{Handler: mux}
	go func() {
		if err := httpSrv.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("filesystem store: http serve", "error", err.Error())
		}
	}()

	var adminSrv *http.Server
	if adminLis != nil {
		adminSrv = &http.Server{Handler: st.AdminHandler()}
		go func() {
			if err := adminSrv.Serve(adminLis); err != nil && err != http.ErrServerClosed {
				slog.Warn("filesystem store: admin serve", "error", err.Error())
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
	return nil
}

// Server implements genv1.ClaimProducerServer.
type Server struct {
	genv1.UnimplementedClaimProducerServer
	store *fsstore.Store
}

// producerDeclaredErrorClasses is the error-class vocabulary the store
// names on the ClaimProducer surface: the google.rpc.ErrorInfo Reason
// stamped on faulted producer verbs (fs/root_unavailable). Advertised
// on CapabilitiesResponse.declared_error_classes so the template
// validator's `error_types:` range-check accepts these keys.
//
// @source: lib/services/stores/postgres/server/server.go:producerDeclaredErrorClasses
// @diverged: true
// @reason: the filesystem store transmits a single class (no swap staging).
func producerDeclaredErrorClasses() []string {
	return []string{
		fsstore.RootUnavailableClass,
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

// Open delegates to the store logic and packages the OpenOutcome
// as the wire-form OpenResponse oneof. Validates `intent` against
// the wire schema (only "r" or "rw") before dispatching, mirroring
// the HTTP bridge's gate so direct-gRPC callers can't bypass the
// check.
func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, fmt.Errorf("filesystem.Open: intent must be \"r\" or \"rw\", got %q", intent)
	}
	outcome, err := s.store.Open(ctx, req.GetClaimId(), req.GetSelector())
	if err != nil {
		return nil, classedStatus(err)
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
// error carries a rimsky error_class (a *fsstore.ClassedError, e.g. the
// `fs/root_unavailable` misconfigured-backing-root rejection), the class
// is stamped into a google.rpc.ErrorInfo detail (Reason = class) — the
// exact shape `lib/runtime/peer.extractErrorClass` decodes — so the
// supervisor routes the producer-verb fault through the operator's
// `error_types:` chain and the control-api surfaces the class + message
// on API-triggered verbs instead of an anonymous failure.
// An unclassed error passes through as a bare Internal status.
//
// @source: lib/services/stores/postgres/server/server.go:classedStatus
func classedStatus(err error) error {
	if err == nil {
		return nil
	}
	var ce *fsstore.ClassedError
	if !errors.As(err, &ce) || ce.Class == "" {
		return err
	}
	st := status.New(codes.Internal, ce.Error())
	withInfo, derr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: ce.Class,
		Domain: "rimsky.store-filesystem",
	})
	if derr != nil {
		return st.Err()
	}
	return withInfo.Err()
}
