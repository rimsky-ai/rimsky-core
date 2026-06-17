// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify Executor: it accepts a
// dispatch and resolves to one of four outcomes (success,
// declared-class error, tagged-success, or async-callback handoff)
// based on the `mode` attribute in the request. It exists to show the
// exact Go wiring a real executor needs — the generated import path,
// the unary-server method signature, how to build the Outcome oneof
// terminal, how to include a tag on the settling Success (whose name
// must appear in declared_tags), how to defer the verdict via
// AwaitAsyncCallback + a goroutine that POSTs the eventual outcome to
// the supervisor's callback URL, and the Capabilities answer the
// dispatch-time attribute gate requires — none of which the prose
// guide can carry.
//
// It is NOT a test double (see test/support/executors/stub for that)
// and NOT a deployable service. Copy this directory, rename the
// module in go.mod, and replace the body of Execute with your work.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// DeclaredTagName is the single tag this executor may include on
// a settling Success/Error/Park outcome's `tags` field. It MUST
// appear in ObservabilityCapabilities.declared_tags (see
// Capabilities) — rimsky rejects emissions of undeclared tags at the
// supervisor's terminal handler and rejects template subscriptions
// referencing undeclared tags at registration. Exported so the
// cross-stack proof can reference it without restating the literal.
//
// @concept: terminal-tag
const DeclaredTagName = "work_started"

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
//   - `mode`         — string, one of {"ok","emit_event","raise_error","async_callback"}.
//     The executor branches on this at dispatch (see Execute).
//   - `count`        — integer with `minimum: 0`. The constraint makes a
//     static template default like `count: -1` a real value
//     violation rimsky's registration gate refuses, exhibiting
//     the "attribute schema validation rejects misshapen
//     attributes" leg of STORY-executor-protocol's acceptance.
//   - `async_ack_id` — string, optional. The async-callback handoff path
//     (mode == "async_callback") returns AwaitAsyncCallback carrying
//     this value as the ack id. A deterministic, test-supplied
//     ack id lets the cross-stack proof correlate the
//     persistent-registry row, the post-restart callback POST, and
//     the eventual terminal/success against one stable identifier.
//
// Exported so the cross-stack proof can compose templates whose static
// defaults satisfy or violate the schema without restating the schema in
// the test.
const ExpectedAttributesSchema = `{
  "type": "object",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["ok", "emit_event", "raise_error", "async_callback"],
      "default": "ok"
    },
    "count": {
      "type": "integer",
      "minimum": 0,
      "default": 0
    },
    "async_ack_id": {
      "type": "string",
      "default": ""
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

// Execute runs one dispatch. The RPC is unary — one ExecuteRequest
// in, one Outcome out — and the Outcome wraps exactly one variant:
// Success, Error, Park, or AwaitAsyncCallback. Per
// TD-execute-rpc-unary there is no stream, no Heartbeat, and no
// per-event chunking; the executor's side of the protocol settles
// the dispatch with a single return value.
//
// This example branches on the resolved `mode` attribute:
//
//   - mode == "raise_error" → return Outcome{Error} carrying
//     error_class = DeclaredErrorClass. Rimsky routes this via the
//     `error_types:` chain on the node (see concept:error-policy),
//     so an operator declaring `error_types: { example/forbidden:
//     { policy: [give_up] } }` drives the node-run to failed under
//     the declared class — proof the routing keys on the executor-
//     declared class, not a generic fallback.
//
//   - mode == "emit_event" → return Outcome{Success} carrying tag
//     DeclaredTagName on Success.Tags. Downstream subscribers fire
//     on `type: terminal/success when: "<tag>" in payload.tags`.
//
//   - mode == "async_callback" → return Outcome{AwaitAsyncCallback}
//     carrying the supplied `async_ack_id` attribute, then spawn a
//     goroutine that, after a short delay, POSTs an
//     AsyncCallbackBody{success:{...}} to
//     `${callback_url}/v1/callback/{async_ack_id}`. The supervisor
//     persists `async_ack_id` on the dispatch row (per
//     TD-persist-async-callback-registry) so the callback handler
//     can correlate even across supervisor restart. The
//     EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE env var lets a test
//     rewrite the `${callback_url}` host (e.g. swap the in-network
//     `rimsky` alias for a host-mapped `127.0.0.1:<port>`) without
//     touching the wire shape the supervisor advertised.
//
//   - anything else (default "ok") → return Outcome{Success} with
//     no tags.
func (e *Executor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	mode := stringAttr(req, "mode")

	switch mode {
	case "raise_error":
		// @concept: error-policy — the Error.error_class wire value is what
		// routes the failure; the operator's `error_types:` chain keys on
		// this exact string.
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: DeclaredErrorClass,
		}}}, nil

	case "emit_event":
		// @concept: terminal-tag — emit the declared tag on the
		// settling Success outcome. Downstream subscribers fire via
		// `type: terminal/success when: "<tag>" in payload.tags`.
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "minimal example: tagged success",
			Tags:          []string{DeclaredTagName},
		}}}, nil

	case "async_callback":
		// @concept: async-callback-persistence — return
		// AwaitAsyncCallback synchronously so the supervisor persists
		// `async_ack_id` to col:rimsky_node_runs.async_ack_id in tx
		// with the transient/await_async signal. The eventual settling
		// verdict travels over the HTTP callback channel below.
		ackID := stringAttr(req, "async_ack_id")
		if ackID == "" {
			// @deliberate: empty ack id is a template / test-author
			// mistake. The supervisor would persist an empty
			// async_ack_id and the callback lookup would never
			// match — surface the cause as an Error terminal so
			// the failure is visible in the audit, not stuck.
			return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
				ErrorClass: DeclaredErrorClass,
			}}}, nil
		}
		if req.GetCallbackUrl() != "" {
			go postAsyncCallback(req.GetCallbackUrl(), ackID, req.GetCancelToken())
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId:           ackID,
			ExpectedCompletionMs: 0,
		}}}, nil
	}

	// @deliberate: default path returns a plain Success. Set
	// Changed=true and fill AttributesDelta (a *structpb.Struct) to
	// write results back — they are validated against the node's
	// attributes schema at commit. The other three settling Outcome
	// variants are built the same way; construct one and pass it as
	// Outcome.Outcome instead of Outcome_Success:
	//
	//	&genv1.Outcome_Error{Error: &genv1.Error{ErrorClass: "example/failed"}}
	//	&genv1.Outcome_Park{Park: &genv1.Park{Reason: genv1.ParkReason_PARK_REASON_SNOOZE}}
	//	&genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{AsyncAckId: "id"}}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "minimal example: success",
	}}}, nil
}

// postAsyncCallback POSTs an AsyncCallbackBody{success:{...}} to
// `${callbackURL}/v1/callback/{ackID}` after a configurable delay.
// Used by the `mode: async_callback` branch of Execute to settle the
// dispatch via the HTTP webhook channel. The bearer token
// (Authorization: Bearer <cancel_token>) is omitted — the supervisor's
// callback handler does not require it for the terminal POST per spec
// §12.4.
//
// EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE is honored when set: the
// host portion of callbackURL is replaced wholesale (e.g.
// "rimsky:9100" → "127.0.0.1:32679"). Cross-stack tests use this to
// dial the supervisor's host-mapped callback port from an executor
// running outside the docker network — the supervisor's advertised
// in-network URL is not reachable from the host.
//
// EXAMPLE_EXECUTOR_ASYNC_CALLBACK_DELAY_MS controls the pre-POST
// delay (default 100ms). The restart-survival cross-stack proof sets
// this to a large value so the rimsky-all-in-one container's restart
// races AHEAD of the goroutine's POST — forcing the callback to be
// delivered (by the test, not by this goroutine) against the
// post-restart supervisor whose in-memory CallbackRegistry is empty.
//
// Errors are logged to stderr rather than surfaced — at this point
// the Execute RPC has already returned AwaitAsyncCallback and the
// dispatch is async; the goroutine cannot synchronously fail the
// RPC. A test that needs to observe failure modes here can read
// stderr or assert the dispatch never reaches `fresh`.
func postAsyncCallback(callbackURL, ackID, cancelToken string) {
	// @deliberate: configurable delay. Defaults to 100ms — enough
	// for the supervisor's in-tx registerAsync + signal-emit
	// (runner_dispatch.go) to commit BEFORE the callback POST
	// arrives. The handler's persistent-registry lookup keys on a
	// row the supervisor's tx has not yet committed if we race
	// ahead. The cross-stack restart-survival proof overrides this
	// to keep the goroutine in sleep until the test drives the
	// post-restart POST itself.
	delay := 100 * time.Millisecond
	if raw := os.Getenv("EXAMPLE_EXECUTOR_ASYNC_CALLBACK_DELAY_MS"); raw != "" {
		if ms, err := time.ParseDuration(raw + "ms"); err == nil {
			delay = ms
		}
	}
	time.Sleep(delay)

	url := strings.TrimRight(callbackURL, "/") + "/v1/callback/" + ackID
	if override := os.Getenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE"); override != "" {
		url = strings.TrimRight(override, "/") + "/v1/callback/" + ackID
	}

	body := map[string]any{
		"success": map[string]any{
			"changed":        false,
			"change_summary": "minimal example: async-callback success",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: marshal async callback body: %v\n", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: build async callback POST %s: %v\n", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cancelToken != "" {
		req.Header.Set("Authorization", "Bearer "+cancelToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: async callback POST %s: %v\n", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "example-executor: async callback POST %s returned %d\n", url, resp.StatusCode)
	}
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
//   - `declared_tags` — the set of tag names this executor may
//     include on a settling Success/Error/Park outcome's `tags`
//     field. Rimsky validates emissions at the supervisor's terminal
//     handler and validates template `subscribes: [{type:
//     terminal/*, when: "<tag>" in payload.tags}]` references at
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
		DeclaredTags:             []string{DeclaredTagName},
		DeclaredErrorClasses:     []string{DeclaredErrorClass},
	}, nil
}
