// Package server adapts the store-internal filesystem Store to the
// rimsky StoreService gRPC + HTTP+JSON bridge.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	fsstore "github.com/fallguy/rimsky/stores/filesystem/store"
	"github.com/fallguy/rimsky/stores/internal/bridge"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// gracefulStopBudget bounds grpcSrv.GracefulStop() so a hung in-flight
// RPC can't strand the server when ctx is cancelled. Mirrors the
// postgres server's pattern (stores/postgres/server/server.go).
const gracefulStopBudget = 5 * time.Second

// Config is the operator-facing config for the filesystem store-service.
type Config struct {
	Root          string
	PickPolicies  map[string]*fsstore.PickPolicy
	SweepInterval time.Duration
	// HTTPBridgeURL is the externally-reachable HTTP base URL for
	// dashboard clients. Surfaced through StoreObservabilityCapabilities.
	// Empty when not declared; the dashboard then falls back to the
	// dispatch endpoint and HTTP-only routes (claims/admin) won't work.
	HTTPBridgeURL string
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
	genv1.RegisterStoreServiceServer(grpcSrv, srv)
	obsSrv := srv.RegisterObservability(grpcSrv, cfg.Root, cfg.PickPolicies)
	obsSrv.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("filesystem store: grpc serve", "error", err.Error())
		}
	}()

	mux := http.NewServeMux()
	bridge.Mount(mux, srv)
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
	// Bound GracefulStop with a timer so a hung RPC doesn't keep the
	// server up indefinitely after ctx cancellation.
	stopTimer := time.AfterFunc(gracefulStopBudget, grpcSrv.Stop)
	grpcSrv.GracefulStop()
	stopTimer.Stop()
	_ = httpSrv.Close()
	if adminSrv != nil {
		_ = adminSrv.Close()
	}
	return nil
}

// Server implements genv1.StoreServiceServer.
type Server struct {
	genv1.UnimplementedStoreServiceServer
	store *fsstore.Store
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	return &genv1.CapabilitiesResponse{
		Capabilities: &genv1.CapabilityStruct{
			WriteSemantics: string(c.WriteSemantics),
		},
	}, nil
}

// Open delegates to the store logic and packages the OpenOutcome
// as the wire-form OpenResponse oneof. Validates `intent` against
// the wire schema (per spec §4.2: only "r" or "rw") before
// dispatching, mirroring the HTTP bridge's gate so direct-gRPC
// callers can't bypass the check.
func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, fmt.Errorf("filesystem.Open: intent must be \"r\" or \"rw\", got %q", intent)
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
			Address: outcome.Result.Address,
			Payload: outcome.Result.Payload,
			Region:  outcome.Result.Region,
		}},
	}, nil
}

// Commit delegates.
func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{}, nil
}

// Abandon delegates.
func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

// Release delegates.
func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetRegion(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}

// Lifecycle events: the filesystem store does not maintain template or
// instance metadata; all six are no-ops returning success. Per
// docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md §4.3.

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
