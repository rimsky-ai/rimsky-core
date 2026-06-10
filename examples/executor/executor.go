// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify Executor: it accepts a dispatch
// and resolves to one of three outcomes (success, declared-class error, or
// success-with-NamedEvent) based on the `mode` attribute in the request. It
// exists to show the exact Go wiring a real executor needs — the generated
// import path, the streaming-server method signature, how to build the
// StreamClose oneof terminal, how to emit a NamedEvent (whose name must
// appear in declared_events), and the Capabilities answer the dispatch-time
// attribute gate requires — none of which the prose guide can carry.
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

// DeclaredEventName is the single NamedEvent name this executor may emit.
// It MUST appear in ObservabilityCapabilities.declared_events (see
// Capabilities) — rimsky rejects emissions of undeclared names at the
// supervisor and rejects template subscriptions to undeclared event names
// at registration. Exported so the cross-stack proof can reference it
// without restating the literal.
const DeclaredEventName = "work_started"

// DeclaredErrorClass is the single error_class this executor may surface
// on Error.error_class. It MUST appear in
// ObservabilityCapabilities.declared_error_classes (see Capabilities) —
// operator `error_types:` policy keys are range-checked against this set
// at template registration so a typo can't silently no-op a policy chain.
// The `<prefix>/<leaf>` hierarchical shape follows the convention every
// bundled executor uses (see concept:signal hierarchical class rule).
const DeclaredErrorClass = "example/forbidden"

// ExpectedAttributesSchema is the JSON Schema describing the executor's
// accepted attribute shape. It is constraining (not the open
// `{"type":"object"}` permissive shape) so the rimsky registration
// validator has something real to reject a misshapen template against:
//
//   - `mode`     — string, one of {"ok","emit_event","raise_error"}.
//     The executor branches on this at dispatch (see Execute).
//   - `count`    — integer with `minimum: 0`. The constraint makes a
//     static template default like `count: -1` a real value
//     violation rimsky's registration gate refuses, exhibiting
//     the "attribute schema validation rejects misshapen
//     attributes" leg of STORY-executor-protocol's acceptance.
//
// Exported so the cross-stack proof can compose templates whose static
// defaults satisfy or violate the schema without restating the schema in
// the test.
const ExpectedAttributesSchema = `{
  "type": "object",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["ok", "emit_event", "raise_error"],
      "default": "ok"
    },
    "count": {
      "type": "integer",
      "minimum": 0,
      "default": 0
    }
  }
}`

// Executor implements the two executor-facing gRPC services:
//
//   - genv1.ExecutorServer              — the required dispatch surface (Execute).
//   - genv1.ExecutorObservabilityServer — optional overall, but its Capabilities
//     RPC is what advertises the accepted attribute shape, declared event
//     names, and declared error classes (see Capabilities).
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
// This example branches on the resolved `mode` attribute:
//
//   - mode == "raise_error" → emit a single StreamClose carrying Error with
//     error_class = DeclaredErrorClass. Rimsky routes this via the
//     `error_types:` chain on the node (see concept:error-type), so an
//     operator declaring `error_types: { example/forbidden: { policy:
//     [give_up] } }` drives the node-run to failed under the declared
//     class — proof the routing keys on the executor-declared class, not
//     a generic fallback.
//
//   - mode == "emit_event" → first emit a NamedEvent whose name is
//     DeclaredEventName (this writes an `event/<name>` row on the
//     instance's event log per concept:signal), then emit a StreamClose
//     carrying Success.
//
//   - anything else (default "ok") → emit a single StreamClose carrying
//     Success. The Heartbeat keep-alive at the top of the stream is also
//     shipped so this example still exhibits the heartbeat shape.
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

	mode := stringAttr(req, "mode")

	switch mode {
	case "raise_error":
		// Surface the declared error class. The Error.error_class wire
		// value is what concept:error-type routes on; the operator's
		// `error_types:` chain keys on this exact string.
		return stream.Send(&genv1.ExecuteEvent{
			Event: &genv1.ExecuteEvent_StreamClose{
				StreamClose: &genv1.StreamClose{
					Outcome: &genv1.StreamClose_Error{
						Error: &genv1.Error{
							ErrorClass: DeclaredErrorClass,
						},
					},
				},
			},
		})

	case "emit_event":
		// Emit a NamedEvent BEFORE the terminal. The supervisor accepts
		// any number of NamedEvent records on the stream as long as the
		// emitted name appears in declared_events; each one persists on
		// rimsky_events under kind `event/<name>` (per concept:signal),
		// visible on GET /v1/events?kind=event/<name>.
		if err := stream.Send(&genv1.ExecuteEvent{
			Event: &genv1.ExecuteEvent_NamedEvent{
				NamedEvent: &genv1.NamedEvent{
					Name:    DeclaredEventName,
					Payload: []byte(`{"phase":"started"}`),
				},
			},
		}); err != nil {
			return err
		}
		// Fall through to Success terminal.
	}

	// Default + emit_event path: emit a single Success terminal.
	// `Changed=false` halts cascade propagation at this node; set
	// Changed=true and fill AttributesDelta (a *structpb.Struct) to write
	// results back — they are validated against the node's attributes
	// schema at commit.
	//
	// The other three terminal outcomes are built the same way; construct
	// one and pass it as StreamClose.Outcome instead of StreamClose_Success:
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

// stringAttr reads a string attribute by name from the dispatch request.
// Returns the empty string when the attribute is absent or non-string;
// callers compare against the declared enum values (see
// ExpectedAttributesSchema) so an absent attribute lands on the default
// branch.
func stringAttr(req *genv1.ExecuteRequest, name string) string {
	attrs := req.GetAttributes()
	if attrs == nil {
		return ""
	}
	fields := attrs.GetFields()
	v, ok := fields[name]
	if !ok || v == nil {
		return ""
	}
	return v.GetStringValue()
}

// Capabilities is the startup handshake (the ExecutorObservability service).
// Returning a permissive open schema would be the simplest answer, but a
// REAL executor advertises:
//
//   - `expected_attributes_schema` — the JSON Schema rimsky merges with the
//     template's `attributes:` block to compute the effective schema. Rimsky
//     refuses an attribute-bearing node whose executor advertises no schema
//     with error_class "executor_schema_unavailable", and at registration-
//     time validation (mode `all` / `available`) refuses a template whose
//     statically-knowable defaults violate the schema.
//
//   - `declared_events` — the set of NamedEvent names this executor may
//     emit. Rimsky validates emissions at the supervisor and validates
//     template `subscribes: [{type: event/<name>}]` references at
//     registration.
//
//   - `declared_error_classes` — the set of error-class paths this executor
//     may surface on Error.error_class. Operator `error_types:` policy keys
//     are range-checked against this set at template registration so a
//     typo can't silently no-op a policy chain. The convention is the
//     hierarchical `<prefix>/<leaf>` shape (per concept:signal); this
//     executor uses the `example/` prefix.
//
// Rimsky reads these at startup via this RPC; templates and per-instance
// policy keys are validated against the cached answer.
func (e *Executor) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(ExpectedAttributesSchema),
		DeclaredEvents:           []string{DeclaredEventName},
		DeclaredErrorClasses:     []string{DeclaredErrorClass},
	}, nil
}
