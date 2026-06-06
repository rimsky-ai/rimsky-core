// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify Executor: it accepts a dispatch
// and returns terminal success. It exists to show the exact Go wiring a real
// executor needs — the generated import path, the streaming-server method
// signature, how to build the StreamClose oneof terminal, and the Capabilities
// answer the dispatch-time attribute gate requires — none of which the prose
// guide can carry.
//
// It is NOT a test double (see test/support/executors/stub for that) and NOT a
// deployable service. Copy this directory, rename the module in go.mod, and
// replace the body of Execute with your work.
package main

import (
	"context"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Executor implements the two executor-facing gRPC services:
//
//   - genv1.ExecutorServer              — the required dispatch surface (Execute).
//   - genv1.ExecutorObservabilityServer — optional overall, but its Capabilities
//     RPC is what advertises the accepted attribute shape (see Capabilities).
//
// Embedding the generated Unimplemented* servers gives forward-compatible
// defaults for every RPC this example does not override, so new RPCs added to
// the protocol never break this type.
type Executor struct {
	genv1.UnimplementedExecutorServer
	genv1.UnimplementedExecutorObservabilityServer
}

// Execute runs one dispatch. The response stream is zero or more Heartbeat /
// NamedEvent records followed by EXACTLY ONE terminal StreamClose; the executor
// MUST close the stream immediately after the StreamClose. A stream that closes
// without a StreamClose is an infrastructure error to the supervisor.
//
// This minimal version emits one Heartbeat and then a Success terminal.
func (e *Executor) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	// Optional keep-alive while work proceeds; safe to omit for fast work.
	// req carries the dispatch: req.GetAttributes() is the (already
	// substituted) template-author input, req.GetDispatchId() keys per-run state.
	if err := stream.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_Heartbeat{
			Heartbeat: &genv1.Heartbeat{Note: fmt.Sprintf("dispatch %s starting", req.GetNodeId())},
		},
	}); err != nil {
		return err
	}

	// ... do the node's work here, reading req.GetAttributes() ...

	// Send EXACTLY ONE terminal. This example uses Success. `Changed=false`
	// halts cascade propagation at this node; set Changed=true and fill
	// AttributesDelta (a *structpb.Struct) to write results back — they are
	// validated against the node's attributes schema at commit.
	//
	// The other three terminal outcomes are built the same way; construct one
	// and pass it as StreamClose.Outcome instead of StreamClose_Success:
	//
	//	&genv1.StreamClose_Error{Error: &genv1.Error{ErrorClass: "example/failed"}}
	//	&genv1.StreamClose_Park{Park: &genv1.Park{Reason: genv1.ParkReason_PARK_REASON_SNOOZE}}
	//	&genv1.StreamClose_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{AsyncAckId: "id"}}
	return stream.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{
				Outcome: &genv1.StreamClose_Success{
					Success: &genv1.Success{
						Changed:       false,
						ChangeSummary: "minimal example: success",
					},
				},
			},
		},
	})
}

// Capabilities is the startup handshake (the ExecutorObservability service).
// Returning a permissive open schema here is REQUIRED for nodes that carry an
// `attributes:` block: the supervisor's dispatch-time attribute gate rejects
// any attribute-bearing node whose executor advertises no schema with
// error_class "executor_schema_unavailable". `{"type":"object"}` (no
// `properties` block) reads as "accept any attributes".
func (e *Executor) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
	}, nil
}
