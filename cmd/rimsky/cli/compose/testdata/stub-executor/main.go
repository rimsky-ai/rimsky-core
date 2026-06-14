// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// stub-executor is a minimal late-bound gRPC executor used by the Pass 6
// acceptance proofs for the `rimsky compose run` verb. It reads
// RIMSKY_AGENT_PORT (the env var the verb's hostagent.SpawnService helper
// sets when it spawns a `--service` binary), binds a gRPC server on
// 127.0.0.1:<port>, and answers Executor.Execute with one of three
// behaviors driven by dispatch-time attributes:
//
//   - default                       — emit a single Success terminal.
//   - attributes.outcome="fail"     — emit a single Error terminal with
//                                     error_class=stub/failed.
//   - attributes.delay_ms=<int>     — time.Sleep for the configured
//                                     duration before terminating
//                                     (lets STORY-live-progress
//                                     interleave a fast and slow
//                                     instance to prove progress lines
//                                     are emitted live, not batched).
//
// This binary is intentionally lighter than examples/executor — no
// schema, no namedevent paths, no permissive open shape — so a copy
// of it in a scenario test compiles fast and contributes minimal
// surface area to debug.
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

// stubErrorClass is the wire error_class the stub emits when the
// dispatch's `outcome` attribute is "fail". Hierarchical
// `<executor>/<leaf>` per concept:signal. Tests that drive an
// expected-failure node assert on this exact string.
const stubErrorClass = "stub/failed"

// executor implements genv1.ExecutorServer for the stub. Per-dispatch
// behavior is read from the request's attributes — there is no
// per-instance configuration.
type executor struct {
	genv1.UnimplementedExecutorServer
}

// Execute branches on the `outcome` and `delay_ms` attributes of the
// dispatch. delay_ms (if positive) gates the terminal emission so a
// slow-node scenario can prove that progress lines appear during the
// sleep rather than only after the dispatch returns. outcome="fail"
// flips the terminal from Success to Error.
func (e executor) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	delay := intAttr(req, "delay_ms")
	if delay > 0 {
		// Bound the sleep at 60s so a malformed attribute cannot wedge
		// the stub forever — the test that overshoots can still drain.
		if delay > 60_000 {
			delay = 60_000
		}
		select {
		case <-time.After(time.Duration(delay) * time.Millisecond):
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	if stringAttr(req, "outcome") == "fail" {
		err := stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: stubErrorClass,
			}}},
		}})
		return err
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "stub executor: success",
		}}},
	}})
}

// observability implements genv1.ExecutorObservabilityServer. The stub
// retains no traces but MUST answer Capabilities with a non-nil
// expected-attributes schema so the dispatch-time attribute-surface
// gate (lib/runtime.resolveAttributes) does not reject attribute-bearing
// nodes with executor_schema_unavailable. The permissive open shape
// `{"type":"object"}` admits any attribute the templates supply.
//
// DeclaredErrorClasses advertises stubErrorClass so a template node
// using `error_types: { stub/failed: { policy: [give_up] } }` passes
// the registration validator's range-check against the executor's
// advertised vocabulary.
type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities reports the no-observability shape with the permissive
// expected-attributes schema and the single declared error class the
// stub may surface.
func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredErrorClasses:     []string{stubErrorClass},
	}, nil
}

// stringAttr reads a string attribute by name from the dispatch
// request. Returns "" when the attribute is absent or not a string.
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

// intAttr reads an integer attribute by name from the dispatch request,
// admitting either a NumberValue (the canonical proto wire shape for
// JSON numbers) or a StringValue parseable as an int (a defensive
// fallback for templates that serialize numbers as strings). Returns 0
// when the attribute is absent or unparseable.
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

	// Shut down on SIGTERM/SIGINT so the verb's drain coordinator can
	// reap the child cleanly (GracefulStop drains in-flight RPCs first;
	// Stop is the fallback if the in-flight set takes too long).
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
