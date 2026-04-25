package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func main() {
	cfg := LoadConfig()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("http-node starting",
		"grpc_port", cfg.GRPCPort,
		"http_port", cfg.HTTPPort,
		"stub_mode", cfg.StubMode,
	)

	s := NewServer(cfg)

	// gRPC listener on the primary port.
	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.GRPCPort))
	if err != nil {
		slog.Error("grpc listen", "error", err.Error())
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	genv1.RegisterNodeExecutorServer(grpcSrv, s)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc serve", "error", err.Error())
		}
	}()

	// HTTP+JSON bridge on a separate port.
	mux := http.NewServeMux()
	mountBridge(mux, s)
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.HTTPPort),
		Handler: mux,
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve", "error", err.Error())
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("http-node stopping")
	grpcSrv.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
