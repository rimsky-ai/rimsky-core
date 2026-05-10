// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky-mcp-control-api is the bundled MCP shim binary entry point.
// Reads CONTROL_API_URL, CONTROL_API_TOKEN, BIND_ADDR, and PORT from
// the environment and starts the JSON-RPC HTTP server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	controlapimcp "github.com/fallguy/rimsky/mcp-servers/control-api"
)

func main() {
	url := os.Getenv(controlapimcp.EnvControlAPIURL)
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	token := os.Getenv(controlapimcp.EnvControlAPIToken)
	bind := os.Getenv(controlapimcp.EnvBindAddr)
	if bind == "" {
		bind = "0.0.0.0"
	}
	port, _ := strconv.Atoi(os.Getenv(controlapimcp.EnvPort))
	if port == 0 {
		port = 8081
	}
	srv, err := controlapimcp.NewServer(controlapimcp.Config{
		ControlAPIURL:   url,
		ControlAPIToken: token,
	})
	if err != nil {
		slog.Error("NewServer", "error", err.Error())
		os.Exit(1)
	}
	addr := fmt.Sprintf("%s:%d", bind, port)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("rimsky-mcp-control-api listening", "addr", addr, "control_api", url)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ListenAndServe", "error", err.Error())
			os.Exit(1)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
