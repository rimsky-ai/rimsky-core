// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/execoutcome"
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
	if stubmode.IsProbe(ud) && s.stubMode {
		return execoutcome.StubSuccess("verifier-shape-checks stub")
	}
	specs, err := parseChecks(ud)
	if err != nil {
		return execoutcome.Errored("verifier/attribute_invalid", err.Error())
	}
	rows, rowsPresent, err := parseRows(ud)
	if err != nil {
		return execoutcome.Errored("verifier/attribute_invalid", err.Error())
	}
	if !rowsPresent {
		if missing := firstRowLevelKind(specs); missing != "" {
			return execoutcome.Errored("verifier/attribute_invalid",
				fmt.Sprintf("attributes.rows (array) required: check %q needs row-level data", missing))
		}
	}
	results := make([]scoredResult, 0, len(specs))
	var configErrors []scoredResult
	blockingFailures := 0
	firstBlockingKind := ""
	for _, spec := range specs {
		r := checks.Run(spec, rows)
		sr := scoredResult{Result: r, Severity: spec.Severity}
		results = append(results, sr)
		if r.ConfigError {
			configErrors = append(configErrors, sr)
			continue
		}
		if r.Pass || spec.Severity != checks.SeverityError {
			continue
		}
		blockingFailures++
		if firstBlockingKind == "" {
			firstBlockingKind = spec.Kind
		}
	}
	if len(configErrors) > 0 {
		return execoutcome.Errored("verifier/attribute_invalid", configErrorMessage(configErrors))
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

func parseRows(ud map[string]any) ([]checks.Row, bool, error) {
	v, present := ud["rows"]
	if !present || v == nil {
		return nil, false, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, false, fmt.Errorf("attributes.rows must be an array, got %T", v)
	}
	out := make([]checks.Row, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("rows[%d] must be an object", i)
		}
		out = append(out, obj)
	}
	return out, true, nil
}

func firstRowLevelKind(specs []checks.CheckSpec) string {
	for _, spec := range specs {
		if checks.RequiresRows(spec.Kind) {
			return spec.Kind
		}
	}
	return ""
}

func configErrorMessage(configErrors []scoredResult) string {
	parts := make([]string, 0, len(configErrors))
	for _, sr := range configErrors {
		parts = append(parts, fmt.Sprintf("%s: %s", sr.Kind, sr.Message))
	}
	return strings.Join(parts, "; ")
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
