// Package server adapts the store-internal stub Store to the rimsky
// StoreService gRPC + HTTP+JSON bridge.
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
	stubstore "github.com/fallguy/rimsky/stores/stub/store"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the server when ctx is cancelled. Mirrors the
// postgres server's pattern (stores/postgres/server/server.go).
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config.
type Config struct {
	Substrate stubstore.Config
}

// Run starts the gRPC + HTTP listeners and serves until ctx is
// cancelled. Mirrors the filesystem signature; postgres adds an admin
// listener (5th arg) for items insertion, while filesystem and stub
// have no admin surface in v3. Callers that need the in-memory
// *stubstore.Store handle (the loopback testfixture) construct it
// themselves and pass it in via RunWithStore.
func Run(ctx context.Context, cfg Config, grpcLis, httpLis net.Listener) error {
	st := stubstore.New(cfg.Substrate)
	return RunWithStore(ctx, st, grpcLis, httpLis)
}

// RunWithStore is the testfixture-friendly entry point: callers pass an
// already-constructed *stubstore.Store so they can keep a handle for
// test assertions while the server's lifetime is bounded by ctx.
func RunWithStore(ctx context.Context, st *stubstore.Store, grpcLis, httpLis net.Listener) error {
	srv := &Server{Store: st}
	grpcSrv := grpc.NewServer()
	genv1.RegisterStoreServiceServer(grpcSrv, srv)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("stub store: grpc serve", "error", err.Error())
		}
	}()
	mux := http.NewServeMux()
	bridge.Mount(mux, srv)
	httpSrv := &http.Server{Handler: mux}
	go func() {
		if err := httpSrv.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("stub store: http serve", "error", err.Error())
		}
	}()
	<-ctx.Done()
	// Bound GracefulStop with a timer so a hung RPC doesn't keep the
	// server up indefinitely after ctx cancellation.
	stopTimer := time.AfterFunc(gracefulStopBudget, grpcSrv.Stop)
	grpcSrv.GracefulStop()
	stopTimer.Stop()
	_ = httpSrv.Close()
	return nil
}

// Server implements genv1.StoreServiceServer.
type Server struct {
	genv1.UnimplementedStoreServiceServer
	Store *stubstore.Store
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.Store.Capabilities()
	return &genv1.CapabilitiesResponse{
		Capabilities: &genv1.CapabilityStruct{WriteSemantics: string(c.WriteSemantics)},
	}, nil
}

// Open delegates. Validates `intent` against the wire schema (per
// spec §4.2: only "r" or "rw") before dispatching, mirroring the HTTP
// bridge's gate so direct-gRPC callers can't bypass the check.
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
			Address: outcome.Result.Address,
			Payload: outcome.Result.Payload,
			Region:  outcome.Result.Region,
		}},
	}, nil
}

// Commit delegates.
func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.Store.Commit(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.Store.Abandon(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.Store.Release(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}

// Lifecycle events: the stub store does not maintain template or
// instance metadata; all six are no-ops returning success. Per
// docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md §4.3.

func (s *Server) OnTemplateRegistered(_ context.Context, _ *genv1.OnTemplateRegisteredRequest) (*genv1.OnTemplateRegisteredResponse, error) {
	return &genv1.OnTemplateRegisteredResponse{}, nil
}

func (s *Server) OnTemplateDeployed(_ context.Context, _ *genv1.OnTemplateDeployedRequest) (*genv1.OnTemplateDeployedResponse, error) {
	return &genv1.OnTemplateDeployedResponse{}, nil
}

func (s *Server) OnTemplateUndeployed(_ context.Context, _ *genv1.OnTemplateUndeployedRequest) (*genv1.OnTemplateUndeployedResponse, error) {
	return &genv1.OnTemplateUndeployedResponse{}, nil
}

func (s *Server) OnTemplateDeregistered(_ context.Context, _ *genv1.OnTemplateDeregisteredRequest) (*genv1.OnTemplateDeregisteredResponse, error) {
	return &genv1.OnTemplateDeregisteredResponse{}, nil
}

func (s *Server) OnInstanceCreated(_ context.Context, _ *genv1.OnInstanceCreatedRequest) (*genv1.OnInstanceCreatedResponse, error) {
	return &genv1.OnInstanceCreatedResponse{}, nil
}

func (s *Server) OnInstanceTerminated(_ context.Context, _ *genv1.OnInstanceTerminatedRequest) (*genv1.OnInstanceTerminatedResponse, error) {
	return &genv1.OnInstanceTerminatedResponse{}, nil
}
