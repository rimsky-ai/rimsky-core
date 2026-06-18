// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
)

type Server struct {
	genv1.UnimplementedExecutorServer
	stubMode bool
}

func NewServer(stubMode bool) *Server { return &Server{stubMode: stubMode} }

func (s *Server) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return s.executeCore(req), nil
}

func (s *Server) executeCore(req *genv1.ExecuteRequest) *genv1.Outcome {
	ud := req.GetAttributes().AsMap()
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return stubSuccess()
	}
	specs, err := parseChecks(ud)
	if err != nil {
		return erroredOutcome("verifier/attribute_invalid", err.Error())
	}
	rows, err := parseRows(ud)
	if err != nil {
		return erroredOutcome("verifier/attribute_invalid", err.Error())
	}
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
			firstBlockingKind = spec.Kind
			if firstBlockingKind == "" {
				firstBlockingKind = r.Kind
			}
		}
	}
	if blockingFailures > 0 {
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: "verifier/check_failed/" + firstBlockingKind,
			Payload:    buildErrorPayload(results),
		}}}
	}
	delta := buildSuccessDelta(results, len(rows))
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   fmt.Sprintf("verifier-shape-checks: %d checks passed (%d rows)", len(results), len(rows)),
	}}}
}

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

type scoredResult struct {
	checks.Result
	Severity checks.Severity
}

func failureEntry(sr scoredResult) map[string]any {
	return map[string]any{
		"kind":     sr.Kind,
		"severity": string(sr.Severity),
		"message":  sr.Message,
		"rows":     float64(sr.Counts.Rows),
		"failed":   float64(sr.Counts.Failed),
	}
}

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

func erroredOutcome(class, msg string) *genv1.Outcome {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class, Payload: payload,
	}}}
}

func stubSuccess() *genv1.Outcome {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   "verifier-shape-checks stub",
	}}}
}

var _ = json.Marshal
