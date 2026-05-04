// Package server adapts the store-internal stub Store to the rimsky
// ClaimProducer + LifecycleSubscriber gRPC + HTTP+JSON bridge.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"github.com/fallguy/rimsky/stores/internal/bridge"
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
	srv := &Server{Store: st}
	grpcSrv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(grpcSrv, srv)
	if cfg.EnableLifecycle {
		genv1.RegisterLifecycleSubscriberServer(grpcSrv, lifecycle.NewServer())
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

// Server implements genv1.ClaimProducerServer.
type Server struct {
	genv1.UnimplementedClaimProducerServer
	Store *stubstore.Store
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.Store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsEnvelope))
	for _, ws := range c.WriteSemanticsEnvelope {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	return &genv1.CapabilitiesResponse{WriteSemanticsEnvelope: out}, nil
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
			Scope:                  outcome.Result.Scope,
			RealizedWriteSemantics: bridge.WriteSemanticsToProto(string(outcome.Result.RealizedWriteSemantics)),
		}},
	}, nil
}

// Commit delegates.
func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(ctx, req.GetClaimId(), req.GetScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(ctx, req.GetClaimId(), req.GetScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.Store.Release(ctx, req.GetClaimId(), req.GetScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}
