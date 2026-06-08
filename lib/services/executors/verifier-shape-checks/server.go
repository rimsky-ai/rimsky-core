// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
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
		return sendErrored(send, "verifier/attribute_invalid", err.Error())
	}
	rows, err := parseRows(ud)
	if err != nil {
		return sendErrored(send, "verifier/attribute_invalid", err.Error())
	}
	// Run each check and pair its Result with the declared Severity so the
	// aggregator can partition failures: only error-severity failures block
	// the commit; warning-severity failures are non-blocking soft findings.
	results := make([]scoredResult, 0, len(specs))
	blockingFailures := 0
	firstBlockingKind := ""
	for _, spec := range specs {
		r := checks.Run(spec, rows)
		results = append(results, scoredResult{Result: r, Severity: spec.Severity})
		if r.Pass || spec.Severity != checks.SeverityError {
			continue
		}
		blockingFailures++
		if firstBlockingKind == "" {
			// Prefer the spec's declared kind; fall back to the
			// runner-reported kind (which is what the "unknown"
			// dispatcher sets when spec.Kind isn't a registered
			// check name).
			firstBlockingKind = spec.Kind
			if firstBlockingKind == "" {
				firstBlockingKind = r.Kind
			}
		}
	}
	if blockingFailures > 0 {
		// Aggregate failure messages in a Struct payload; the
		// rimsky-side error_class policy fires on the hierarchical
		// `verifier/check_failed/<kind>` leaf per `concept:signal`. The
		// payload also carries any warning-severity failures so the
		// operator sees the full picture even when an error blocks.
		payload := buildErrorPayload(results)
		return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: "verifier/check_failed/" + firstBlockingKind,
				Payload:    payload,
			}}},
		}})
	}
	// No blocking failure: the dispatch succeeds. Any warning-severity
	// failure is surfaced as a non-blocking finding in the Success delta so
	// the operator still sees the soft signal.
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
		severity, err := parseSeverity(obj["severity"], i)
		if err != nil {
			return nil, err
		}
		out = append(out, checks.CheckSpec{Kind: kind, Config: cfg, Severity: severity})
	}
	return out, nil
}

// parseSeverity reads a `checks[i].severity` value into the services-local
// checks.Severity. An absent / empty value defaults to checks.SeverityError
// (a failing check blocks unless the author explicitly downgrades it). An
// unknown string is rejected with an attribute error rather than silently
// coerced — protecting against a typo'd "warn"/"err" quietly flipping a
// check's blocking behavior the operator did not intend.
func parseSeverity(raw any, i int) (checks.Severity, error) {
	s, _ := raw.(string)
	switch checks.Severity(s) {
	case "":
		return checks.SeverityError, nil
	case checks.SeverityError:
		return checks.SeverityError, nil
	case checks.SeverityWarning:
		return checks.SeverityWarning, nil
	}
	return "", fmt.Errorf("checks[%d].severity %q invalid (want %q or %q)",
		i, s, checks.SeverityError, checks.SeverityWarning)
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

// scoredResult pairs a check Result with the declared Severity that
// governs whether its failure blocks the commit. The aggregation,
// payload-building, and delta-building paths all read severity off this
// pair so a warning-severity failure is never miscounted as blocking.
type scoredResult struct {
	checks.Result
	Severity checks.Severity
}

// failureEntry renders one failed check (blocking or warning) into the
// Struct-shaped finding surfaced in both the error payload and the
// success-delta warnings list.
func failureEntry(sr scoredResult) map[string]any {
	return map[string]any{
		"kind":     sr.Kind,
		"severity": string(sr.Severity),
		"message":  sr.Message,
		"rows":     float64(sr.Counts.Rows),
		"failed":   float64(sr.Counts.Failed),
	}
}

// buildErrorPayload turns failed results into the
// `Error.payload` Struct surfaced upstream. Blocking (error-severity)
// failures populate `failures`; warning-severity failures are split into
// `warnings` so the consumer can tell the soft findings from the blocking
// ones even on the error path.
func buildErrorPayload(results []scoredResult) *structpb.Struct {
	failures := make([]any, 0, len(results))
	warnings := make([]any, 0)
	for _, sr := range results {
		if sr.Pass {
			continue
		}
		if sr.Severity == checks.SeverityWarning {
			warnings = append(warnings, failureEntry(sr))
			continue
		}
		failures = append(failures, failureEntry(sr))
	}
	st, _ := structpb.NewStruct(map[string]any{
		"failures": failures,
		"warnings": warnings,
		"summary":  summarize(results),
	})
	return st
}

// buildSuccessDelta builds the Success attributes_delta. When no blocking
// failure occurred but one or more warning-severity checks failed, those
// soft findings are surfaced under `verifier_warnings` (with a count) so
// the non-blocking signal is observable to the operator.
func buildSuccessDelta(results []scoredResult, rowsLen int) *structpb.Struct {
	warnings := make([]any, 0)
	for _, sr := range results {
		if !sr.Pass && sr.Severity == checks.SeverityWarning {
			warnings = append(warnings, failureEntry(sr))
		}
	}
	fields := map[string]any{
		"verifier_pass":          true,
		"verifier_checks":        float64(len(results)),
		"verifier_rows":          float64(rowsLen),
		"verifier_warning_count": float64(len(warnings)),
		"verifier_warnings":      warnings,
	}
	out, _ := structpb.NewStruct(fields)
	return out
}

func summarize(results []scoredResult) string {
	parts := make([]string, 0, len(results))
	for _, sr := range results {
		mark := "OK"
		if !sr.Pass {
			mark = "FAIL"
			if sr.Severity == checks.SeverityWarning {
				mark = "WARN"
			}
		}
		parts = append(parts, fmt.Sprintf("%s=%s", sr.Kind, mark))
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
