// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const gracefulStopBudget = 5 * time.Second

type Config struct {
	Connection      string
	WriteSemantics  claimproducer.WriteSemantics
	PickPolicies    map[string]*pgsstore.PickPolicy
	SweepInterval   time.Duration
	HTTPBridgeURL   string
	EnableLifecycle bool
	// @concept: executor
	EnableExecutor bool
}

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

type Server struct {
	genv1.UnimplementedClaimProducerServer
	store *pgsstore.Store
}

func producerDeclaredErrorClasses() []string {
	return []string{
		pgsstore.ClaimUnavailableClass,
		pgsstore.SwapFailedClass,
	}
}

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
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.CommitResponse{}, nil
}

func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.AbandonResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.ReleaseResponse{}, nil
}

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
