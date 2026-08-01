// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const stubErrorClass = "stub/failed"

type executor struct {
	genv1.UnimplementedExecutorServer
}

func (e executor) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	delay := intAttr(req, "delay_ms")
	if delay > 0 {
		if delay > 60_000 {
			delay = 60_000
		}
		select {
		case <-time.After(time.Duration(delay) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if stringAttr(req, "outcome") == "fail" {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: stubErrorClass,
		}}}, nil
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "stub executor: success",
	}}}, nil
}

type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredErrorClasses:     []string{stubErrorClass},
	}, nil
}

func stringAttr(req *genv1.ExecuteRequest, name string) string {
	attrs := req.GetAttributes()
	if attrs == nil {
		return ""
	}
	v, ok := attrs.GetFields()[name]
	if !ok || v == nil {
		return ""
	}
	return v.GetStringValue()
}

func intAttr(req *genv1.ExecuteRequest, name string) int {
	attrs := req.GetAttributes()
	if attrs == nil {
		return 0
	}
	v, ok := attrs.GetFields()[name]
	if !ok || v == nil {
		return 0
	}
	if n := v.GetNumberValue(); n != 0 {
		return int(n)
	}
	if s := v.GetStringValue(); s != "" {
		i, err := strconv.Atoi(s)
		if err == nil {
			return i
		}
	}
	return 0
}

func main() {
	portStr := os.Getenv("RIMSKY_AGENT_PORT")
	if portStr == "" {
		fmt.Fprintln(os.Stderr, "stub-executor: RIMSKY_AGENT_PORT not set")
		os.Exit(2)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stub-executor: invalid RIMSKY_AGENT_PORT %q: %v\n", portStr, err)
		os.Exit(2)
	}
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stub-executor: listen 127.0.0.1:%d: %v\n", port, err)
		os.Exit(2)
	}

	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, executor{})
	genv1.RegisterExecutorObservabilityServer(srv, observability{})
	slog.Info("stub-executor listening", "port", port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		done := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			srv.Stop()
		}
	}()

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "stub-executor: serve: %v\n", err)
		os.Exit(1)
	}
}
