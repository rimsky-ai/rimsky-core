// Package server adapts the store-internal postgres Store to the
// rimsky StoreService gRPC + HTTP+JSON bridge.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	corestore "github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/stores/internal/bridge"
	pgsstore "github.com/fallguy/rimsky/stores/postgres/store"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
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
	genv1.RegisterStoreServiceServer(grpcSrv, srv)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("postgres store: grpc serve", "error", err.Error())
		}
	}()

	httpMux := http.NewServeMux()
	bridge.Mount(httpMux, srv)
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
	// Bound GracefulStop with a timer so a hung RPC doesn't keep the
	// store's pool open indefinitely. After the budget elapses,
	// drop to the hard Stop. This also lets defer st.Close() at the
	// end of Run actually run in bounded time.
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

// Server implements genv1.StoreServiceServer.
type Server struct {
	genv1.UnimplementedStoreServiceServer
	store *pgsstore.Store
}

// Capabilities returns the store's advertised capability struct.
func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	return &genv1.CapabilitiesResponse{
		Capabilities: &genv1.CapabilityStruct{WriteSemantics: string(c.WriteSemantics)},
	}, nil
}

// Open delegates.
func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
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
