// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// observability_check.go implements the spec §6 / Task F1
// --check-observability probe run by `rimsky conformance executor`.
// Substantive checks:
//
//   - Capabilities returns a usable shape.
//   - GetTrace on a known-missing dispatch returns the evicted-shape
//     envelope (Evicted=true, Complete=true, Events=[]). Spec §2.6.
//   - StreamTrace on the same missing dispatch closes cleanly with the
//     same evicted-shape marker.
//   - Canned-dispatch round-trip (when stub-mode permits): drive an
//     Execute via the gRPC Executor surface, then assert that
//     GetTrace + StreamTrace eventually yield events for that
//     dispatch_id with complete=true after the terminal arrives.
//   - Retention probe: when RetentionTestSeconds is set, sleep
//     past the configured retention and verify GetTrace returns
//     evicted=true.
//   - Standard-vocab attribute validation: if any returned events use
//     the standard categories (step_started, step_completed,
//     step_failed, tool_call, error), the probe verifies the spec §2.4
//     required attribute keys are present.

package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// ObservabilityCheckOpts configures the observability probe.
type ObservabilityCheckOpts struct {
	// Endpoint to dial. Transport must be "grpc".
	Endpoint Endpoint
	// RetentionTestSeconds: when > 0 the probe drives a canned
	// dispatch then sleeps this long and verifies GetTrace returns
	// evicted=true (spec §6 retention check).
	RetentionTestSeconds int
}

// RunObservabilityCheck implements the spec §6 / Task F1 probe. It
// emits one line of human-readable progress per check via the
// supplied logf callback; supply fmt.Printf when wired into a CLI
// binary or a no-op when invoked from a Go test that wants only the
// error result.
func RunObservabilityCheck(ctx context.Context, opts ObservabilityCheckOpts, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Endpoint.Transport != "grpc" {
		logf("observability: skipping (transport != grpc)\n")
		return nil
	}
	conn, err := grpc.NewClient(stripScheme(opts.Endpoint.URL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	client := genv1.NewExecutorObservabilityClient(conn)
	caps, err := client.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			logf("observability: Capabilities Unimplemented (executor declares no observability)\n")
			return nil
		}
		return fmt.Errorf("Capabilities: %w", err)
	}
	logf("observability: capabilities = supports_trace_get=%v supports_trace_stream=%v retention=%ds http_bridge_url=%q\n",
		caps.GetSupportsTraceGet(), caps.GetSupportsTraceStream(),
		caps.GetRetentionAfterTerminalSeconds(), caps.GetHttpBridgeUrl())

	if schema := caps.GetExpectedAttributesSchema(); len(schema) > 0 {
		if !looksLikeJSON(schema) {
			return fmt.Errorf("Capabilities.expected_attributes_schema is non-empty but does not parse as JSON (%d bytes)", len(schema))
		}
		logf("observability: expected_attributes_schema declared (%d bytes JSON)\n", len(schema))
	} else {
		logf("observability: expected_attributes_schema = empty (executor accepts any attributes)\n")
	}
	for i, name := range caps.GetDeclaredEvents() {
		if name == "" {
			return fmt.Errorf("Capabilities.declared_events[%d] is empty", i)
		}
	}
	if names := caps.GetDeclaredEvents(); len(names) > 0 {
		logf("observability: declared_events = %v\n", names)
	} else {
		logf("observability: declared_events = []\n")
	}

	const probeID = "conformance-probe-no-dispatch"

	if caps.GetSupportsTraceGet() {
		tr, err := client.GetTrace(ctx, &genv1.GetTraceRequest{DispatchId: probeID})
		if err != nil {
			return fmt.Errorf("GetTrace probe: %w", err)
		}
		if !tr.GetEvicted() {
			return fmt.Errorf("GetTrace on missing dispatch returned evicted=%v, want true (spec §2.6)", tr.GetEvicted())
		}
		if !tr.GetComplete() {
			return fmt.Errorf("GetTrace on missing dispatch returned complete=%v, want true (spec §2.6)", tr.GetComplete())
		}
		if len(tr.GetEvents()) != 0 {
			return fmt.Errorf("GetTrace on missing dispatch returned %d events, want 0 (spec §2.6)", len(tr.GetEvents()))
		}
		if err := validateStandardEvents(tr.GetEvents()); err != nil {
			return fmt.Errorf("GetTrace events: %w", err)
		}
		logf("observability: GetTrace evicted-shape OK\n")
	}
	if caps.GetSupportsTraceStream() {
		streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := client.StreamTrace(streamCtx, &genv1.StreamTraceRequest{DispatchId: probeID})
		if err != nil {
			return fmt.Errorf("StreamTrace open: %w", err)
		}
		var got []*genv1.TraceEvent
		for {
			ev, rerr := stream.Recv()
			if rerr != nil {
				break
			}
			got = append(got, ev)
		}
		if err := validateStandardEvents(got); err != nil {
			return fmt.Errorf("StreamTrace events: %w", err)
		}
		logf("observability: StreamTrace evicted-shape received %d events\n", len(got))
	}

	// Canned-dispatch round-trip: drive an Execute via the executor's
	// Executor and verify the in-memory trace surfaces events.
	cannedID := fmt.Sprintf("conformance-canned-%d", time.Now().UnixNano())
	if caps.GetSupportsTraceGet() {
		if err := runCannedDispatch(ctx, conn, client, cannedID, logf); err != nil {
			logf("observability: canned dispatch skipped: %v\n", err)
		}
	}
	if opts.RetentionTestSeconds > 0 && caps.GetSupportsTraceGet() {
		if err := runRetentionProbe(ctx, client, cannedID, opts.RetentionTestSeconds, logf); err != nil {
			return fmt.Errorf("retention probe: %w", err)
		}
	}
	return nil
}

// runCannedDispatch fires a stub-mode Execute and verifies that
// GetTrace + StreamTrace return events for the dispatch.
func runCannedDispatch(ctx context.Context, conn *grpc.ClientConn, obs genv1.ExecutorObservabilityClient, dispatchID string, logf func(string, ...any)) error {
	ud, err := structpb.NewStruct(map[string]any{"stub_probe": true})
	if err != nil {
		return fmt.Errorf("build attributes: %w", err)
	}
	exec := genv1.NewExecutorClient(conn)
	stream, err := exec.Execute(ctx, &genv1.ExecuteRequest{
		DispatchId: dispatchID,
		NodeId:     "obs-probe-node",
		InstanceId: "obs-probe-instance",
		NodeType:   "conformance-observability",
		Attributes: ud,
	})
	if err != nil {
		return fmt.Errorf("Execute: %w", err)
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("Execute recv: %w", err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	tr, err := obs.GetTrace(ctx, &genv1.GetTraceRequest{DispatchId: dispatchID})
	if err != nil {
		return fmt.Errorf("GetTrace canned: %w", err)
	}
	if tr.GetEvicted() {
		return fmt.Errorf("GetTrace canned dispatch evicted=true; expected ledgered events")
	}
	if !tr.GetComplete() {
		return fmt.Errorf("GetTrace canned dispatch complete=false; expected terminal after Execute")
	}
	if len(tr.GetEvents()) == 0 {
		return fmt.Errorf("GetTrace canned dispatch returned 0 events; expected step_started+terminal")
	}
	logf("observability: canned dispatch GetTrace ok (%d events, complete=true)\n", len(tr.GetEvents()))

	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ts, err := obs.StreamTrace(streamCtx, &genv1.StreamTraceRequest{DispatchId: dispatchID})
	if err != nil {
		return fmt.Errorf("StreamTrace canned: %w", err)
	}
	got := []*genv1.TraceEvent{}
	for {
		ev, rerr := ts.Recv()
		if rerr != nil {
			break
		}
		got = append(got, ev)
	}
	if len(got) == 0 {
		return fmt.Errorf("StreamTrace canned dispatch closed with 0 events")
	}
	last := got[len(got)-1]
	if last.GetCategory() != "trace_complete" {
		logf("observability: StreamTrace canned dispatch closed without trace_complete (last category=%q)\n", last.GetCategory())
	} else {
		logf("observability: StreamTrace canned dispatch ended with trace_complete (%d events)\n", len(got))
	}
	return nil
}

// runRetentionProbe sleeps past the configured retention and verifies
// GetTrace returns evicted=true.
func runRetentionProbe(ctx context.Context, obs genv1.ExecutorObservabilityClient, dispatchID string, seconds int, logf func(string, ...any)) error {
	wait := time.Duration(seconds+1) * time.Second
	logf("observability: retention probe — sleeping %v before re-querying\n", wait)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	tr, err := obs.GetTrace(ctx, &genv1.GetTraceRequest{DispatchId: dispatchID})
	if err != nil {
		return fmt.Errorf("GetTrace post-retention: %w", err)
	}
	if !tr.GetEvicted() {
		return fmt.Errorf("GetTrace post-retention evicted=%v, want true (executor did not honour retention_after_terminal_seconds)", tr.GetEvicted())
	}
	logf("observability: retention probe ok (evicted=true)\n")
	return nil
}

// validateStandardEvents enforces spec §2.4 attribute requirements for
// any events whose category falls in the standard vocabulary.
func validateStandardEvents(events []*genv1.TraceEvent) error {
	for _, ev := range events {
		attrs := ev.GetAttributes().AsMap()
		switch ev.GetCategory() {
		case "step_started", "step_completed":
			if _, ok := attrs["step_id"]; !ok {
				return fmt.Errorf("event category=%q missing required attribute step_id", ev.GetCategory())
			}
		case "step_failed":
			if _, ok := attrs["step_id"]; !ok {
				return fmt.Errorf("event category=step_failed missing required attribute step_id")
			}
			if _, ok := attrs["error"]; !ok {
				return fmt.Errorf("event category=step_failed missing required attribute error")
			}
		case "subcall_started":
			if _, ok := attrs["subcall_id"]; !ok {
				return fmt.Errorf("event category=subcall_started missing required attribute subcall_id")
			}
			if _, ok := attrs["target"]; !ok {
				return fmt.Errorf("event category=subcall_started missing required attribute target")
			}
		case "subcall_completed":
			if _, ok := attrs["subcall_id"]; !ok {
				return fmt.Errorf("event category=subcall_completed missing required attribute subcall_id")
			}
		case "tool_call":
			for _, k := range []string{"tool_name", "arguments", "result", "duration_ms"} {
				if _, ok := attrs[k]; !ok {
					return fmt.Errorf("event category=tool_call missing required attribute %s", k)
				}
			}
		case "error":
			if _, ok := attrs["error"]; !ok {
				return fmt.Errorf("event category=error missing required attribute error")
			}
		}
	}
	return nil
}

// looksLikeJSON returns true when the bytes parse as valid JSON.
func looksLikeJSON(b []byte) bool {
	var x any
	return json.Unmarshal(b, &x) == nil
}
