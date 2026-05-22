// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — verifier-shape-checks bundled verifier executor.
// Implements the rimsky Executor protocol; runs N shape-check
// primitives (no_nulls, pk_unique, etc.) over an in-memory `rows`
// payload and returns a Success or Error terminal based on aggregate
// pass/fail.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Verifier executors / verifier-shape-checks.
//
// @concept: verifier-pattern
//
// Attribute schema (passed at Execute time via ExecuteRequest.attributes):
//
//	{
//	  "checks": [
//	    {"kind": "no_nulls", "config": {"field": "id"}},
//	    {"kind": "pk_unique", "config": {"field": "id"}},
//	    ...
//	  ],
//	  "rows": [{...}, {...}]      // tabular payload to verify
//	}
//
// The `rows` field is provided by the caller (substitution layer pulls
// it from the upstream claim's address via `{{...}}` resolution in the
// template). The executor never reads the upstream claim itself.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/executors/verifier-shape-checks/checks"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Server implements genv1.ExecutorServer. Stateless; no per-server
// configuration besides the stub-mode flag (consumed by the
// conformance probe).
type Server struct {
	genv1.UnimplementedExecutorServer
	stubMode bool
}

// NewServer constructs a Server with the optional stub-mode flag.
func NewServer(stubMode bool) *Server { return &Server{stubMode: stubMode} }

// Execute is the gRPC entrypoint. Adapts to the transport-neutral
// `executeCore`.
func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return s.executeCore(req, stream.Send)
}

// sendFunc is the narrow sender shared with executeCore; same shape
// as http-node's transport seam.
type sendFunc func(*genv1.ExecuteEvent) error

// executeCore parses the attribute bag, runs the configured checks
// against the rows payload, and emits a single StreamClose terminal.
func (s *Server) executeCore(req *genv1.ExecuteRequest, send sendFunc) error {
	ud := req.GetAttributes().AsMap()
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return send(stubSuccess())
	}
	specs, err := parseChecks(ud)
	if err != nil {
		return sendErrored(send, "invalid_attribute", err.Error())
	}
	rows, err := parseRows(ud)
	if err != nil {
		return sendErrored(send, "invalid_attribute", err.Error())
	}
	results := make([]checks.Result, 0, len(specs))
	failed := 0
	for _, spec := range specs {
		r := checks.Run(spec, rows)
		results = append(results, r)
		if !r.Pass {
			failed++
		}
	}
	if failed > 0 {
		// Aggregate failure messages in a Struct payload; the
		// rimsky-side error_class policy fires on `verifier_failed`.
		payload := buildErrorPayload(results)
		return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: "verifier_failed",
				Payload:    payload,
			}}},
		}})
	}
	delta := buildSuccessDelta(results, len(rows))
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   fmt.Sprintf("verifier-shape-checks: %d checks passed (%d rows)", len(results), len(rows)),
		}}},
	}})
}

// parseChecks reads `attributes.checks` and validates each entry into a
// CheckSpec. Missing / wrong-type → InvalidAttribute.
func parseChecks(ud map[string]any) ([]checks.CheckSpec, error) {
	raw, ok := ud["checks"].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("attributes.checks (non-empty array) required")
	}
	out := make([]checks.CheckSpec, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("checks[%d] must be an object", i)
		}
		kind, _ := obj["kind"].(string)
		if kind == "" {
			return nil, fmt.Errorf("checks[%d].kind required", i)
		}
		cfg, _ := obj["config"].(map[string]any)
		out = append(out, checks.CheckSpec{Kind: kind, Config: cfg})
	}
	return out, nil
}

// parseRows reads `attributes.rows` and normalizes each entry into a
// `checks.Row`. Missing rows is acceptable (some checks like
// row_count_absolute fire on empty input as a pass/fail signal).
func parseRows(ud map[string]any) ([]checks.Row, error) {
	raw, ok := ud["rows"].([]any)
	if !ok {
		return nil, nil
	}
	out := make([]checks.Row, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rows[%d] must be an object", i)
		}
		out = append(out, obj)
	}
	return out, nil
}

// buildErrorPayload turns failed results into the
// `Error.payload` Struct surfaced upstream.
func buildErrorPayload(results []checks.Result) *structpb.Struct {
	failures := make([]any, 0, len(results))
	for _, r := range results {
		if r.Pass {
			continue
		}
		entry := map[string]any{
			"kind":    r.Kind,
			"message": r.Message,
			"rows":    float64(r.Counts.Rows),
			"failed":  float64(r.Counts.Failed),
		}
		failures = append(failures, entry)
	}
	st, _ := structpb.NewStruct(map[string]any{
		"failures": failures,
		"summary":  summarize(results),
	})
	return st
}

func buildSuccessDelta(results []checks.Result, rowsLen int) *structpb.Struct {
	out, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_checks": float64(len(results)),
		"verifier_rows":   float64(rowsLen),
	})
	return out
}

func summarize(results []checks.Result) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		mark := "OK"
		if !r.Pass {
			mark = "FAIL"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", r.Kind, mark))
	}
	return strings.Join(parts, ", ")
}

// sendErrored emits a one-shot Error StreamClose; mirrors http-node's
// helper to keep parity.
func sendErrored(send sendFunc, class, msg string) error {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
			ErrorClass: class, Payload: payload,
		}}},
	}})
}

// stubSuccess returns a fixed Success terminal for the conformance probe.
func stubSuccess() *genv1.ExecuteEvent {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   "verifier-shape-checks stub",
		}}},
	}}
}

// keepCompilerHappy retains references that linters might otherwise
// classify as unused.
var _ = json.Marshal
