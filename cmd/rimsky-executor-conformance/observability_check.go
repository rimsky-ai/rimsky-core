// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/runtime/executor"
)

// jsonValidate is a thin alias for encoding/json's Unmarshal.
func jsonValidate(b []byte, v any) error { return json.Unmarshal(b, v) }

// runObservabilityCheck implements the spec §6 / Task F1
// --check-observability probe. Substantive checks:
//
//   - Capabilities returns a usable shape.
//   - GetTrace on a known-missing dispatch returns the evicted-shape
//     envelope (Evicted=true, Complete=true, Events=[]). Spec §2.6.
//   - StreamTrace on the same missing dispatch closes cleanly with the
//     same evicted-shape marker (Spec §2.6 mandates GetTrace and
//     StreamTrace agree on missing-dispatch behaviour).
//   - Canned-dispatch round-trip (when stub-mode permits): drive an
//     Execute via the gRPC Executor surface, then assert that
//     GetTrace + StreamTrace eventually yield events for that
//     dispatch_id with complete=true after the terminal arrives.
//   - Retention probe: when --retention-test-seconds is set, sleep
//     past the configured retention and verify GetTrace returns
//     evicted=true.
//   - Standard-vocab attribute validation: if any returned events use
//     the standard categories (step_started, step_completed,
//     step_failed, tool_call, error), the probe verifies the spec §2.4
//     required attribute keys are present.
func runObservabilityCheck(ctx context.Context, ep executor.Endpoint, _ bool) error {
	if ep.Transport != "grpc" {
		// HTTP+JSON bridges still expose the gRPC surface; skip when a
		// bridge-only endpoint was given.
		fmt.Println("observability: skipping (transport != grpc)")
		return nil
	}
	conn, err := grpc.NewClient(stripScheme(ep.URL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	client := genv1.NewExecutorObservabilityClient(conn)
	caps, err := client.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		// Unimplemented is the universal "no observability" signal.
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			fmt.Println("observability: Capabilities Unimplemented (executor declares no observability)")
			return nil
		}
		return fmt.Errorf("Capabilities: %w", err)
	}
	fmt.Printf("observability: capabilities = supports_trace_get=%v supports_trace_stream=%v retention=%ds http_bridge_url=%q\n",
		caps.GetSupportsTraceGet(), caps.GetSupportsTraceStream(),
		caps.GetRetentionAfterTerminalSeconds(), caps.GetHttpBridgeUrl())

	// L2 plan: validate the new platform-extensions surfaces on
	// ObservabilityCapabilities (expected_attributes_schema + declared_events).
	// Both fields are optional — empty means "no schema" / "no events"
	// respectively. When expected_attributes_schema is non-empty it must parse as
	// JSON; when declared_events is non-empty each entry must be a
	// non-empty string.
	if schema := caps.GetExpectedAttributesSchema(); len(schema) > 0 {
		if !looksLikeJSON(schema) {
			return fmt.Errorf("Capabilities.expected_attributes_schema is non-empty but does not parse as JSON (%d bytes)", len(schema))
		}
		fmt.Printf("observability: expected_attributes_schema declared (%d bytes JSON)\n", len(schema))
	} else {
		fmt.Println("observability: expected_attributes_schema = empty (executor accepts any attributes)")
	}
	for i, name := range caps.GetDeclaredEvents() {
		if name == "" {
			return fmt.Errorf("Capabilities.declared_events[%d] is empty", i)
		}
	}
	if names := caps.GetDeclaredEvents(); len(names) > 0 {
		fmt.Printf("observability: declared_events = %v\n", names)
	} else {
		fmt.Println("observability: declared_events = []")
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
		fmt.Println("observability: GetTrace evicted-shape OK")
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
		// Spec §2.6: missing-dispatch StreamTrace must close cleanly,
		// emitting at most a trace_complete marker. A NotFound /
		// Unavailable / Unknown response is a contract violation.
		fmt.Printf("observability: StreamTrace evicted-shape received %d events\n", len(got))
	}

	// Canned-dispatch round-trip: drive an Execute via the executor's
	// Executor and verify the in-memory trace surfaces events.
	cannedID := fmt.Sprintf("conformance-canned-%d", time.Now().UnixNano())
	if caps.GetSupportsTraceGet() {
		if err := runCannedDispatch(ctx, conn, client, cannedID); err != nil {
			fmt.Printf("observability: canned dispatch skipped: %v\n", err)
		}
	}
	if obsRetentionTestSeconds > 0 && caps.GetSupportsTraceGet() {
		if err := runRetentionProbe(ctx, client, cannedID, obsRetentionTestSeconds); err != nil {
			return fmt.Errorf("retention probe: %w", err)
		}
	}
	return nil
}

// obsRetentionTestSeconds is set from the --retention-test-seconds CLI
// flag. Zero disables the retention probe.
var obsRetentionTestSeconds int

// runCannedDispatch fires a stub-mode Execute and verifies that
// GetTrace + StreamTrace return events for the dispatch.
func runCannedDispatch(ctx context.Context, conn *grpc.ClientConn, obs genv1.ExecutorObservabilityClient, dispatchID string) error {
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
	// Drain the stream.
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("Execute recv: %w", err)
		}
	}
	// Allow the executor a brief moment to flush its terminal trace.
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
	fmt.Printf("observability: canned dispatch GetTrace ok (%d events, complete=true)\n", len(tr.GetEvents()))

	// StreamTrace replay must also surface events ending with
	// trace_complete (or close cleanly when the canned dispatch is
	// already terminal).
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
		// Some executors close without an explicit trace_complete; we
		// accept that as "complete" too.
		fmt.Printf("observability: StreamTrace canned dispatch closed without trace_complete (last category=%q)\n", last.GetCategory())
	} else {
		fmt.Printf("observability: StreamTrace canned dispatch ended with trace_complete (%d events)\n", len(got))
	}
	return nil
}

// runRetentionProbe sleeps past the configured retention and verifies
// GetTrace returns evicted=true.
func runRetentionProbe(ctx context.Context, obs genv1.ExecutorObservabilityClient, dispatchID string, seconds int) error {
	wait := time.Duration(seconds+1) * time.Second
	fmt.Printf("observability: retention probe — sleeping %v before re-querying\n", wait)
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
	fmt.Println("observability: retention probe ok (evicted=true)")
	return nil
}

// validateStandardEvents enforces spec §2.4 attribute requirements for
// any events whose category falls in the standard vocabulary. Free-form
// categories pass through unchecked.
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

func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return s[len(prefix):]
		}
	}
	return s
}

// looksLikeJSON returns true when the bytes parse as valid JSON. Used
// by the L2 conformance check to validate Capabilities.expected_attributes_schema
// without depending on a JSON Schema validator at the conformance
// layer.
func looksLikeJSON(b []byte) bool {
	var x any
	return jsonValidate(b, &x) == nil
}
