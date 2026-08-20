// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

// @story: verifier-shape-checks
// @story: verifier-severity-partition
func TestVerifierSeverityPartition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
	)

	inBoundsTemplate := buildSeverityPartitionTemplate(
		"severity-partition-in-bounds",
		[]map[string]any{
			{"id": "alpha", "value": float64(10)},
			{"value": float64(20)},
			{"id": "gamma", "value": float64(30)},
		},
	)
	inBoundsTID := ep.DeployTemplate(t, inBoundsTemplate)
	inBoundsIID := ep.CreateInstance(t, inBoundsTID, "ck-severity-in-bounds", "severity-partition")
	requireVerifierSucceededWithWarning(t, ep, inBoundsIID, "verifier")

	outOfBoundsTemplate := buildSeverityPartitionTemplate(
		"severity-partition-out-of-bounds",
		[]map[string]any{
			{"id": "alpha", "value": float64(10)},
			{"value": float64(20)},
			{"id": "gamma", "value": float64(250)},
		},
	)
	outOfBoundsTID := ep.DeployTemplate(t, outOfBoundsTemplate)
	outOfBoundsIID := ep.CreateInstance(t, outOfBoundsTID, "ck-severity-out-of-bounds", "severity-partition")
	requireVerifierFailedWithCheckFailedClass(t, ep, outOfBoundsIID, "verifier")
}

func buildSeverityPartitionTemplate(name string, rows []map[string]any) map[string]any {
	rowsAny := make([]any, len(rows))
	for i, r := range rows {
		rowsAny[i] = r
	}
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "verifier",
					"executor": "verifier-shape-checks",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"checks": map[string]any{
									"type":        "array",
									"description": "shape checks to run against rows; severities straddle the warning/error partition",
									"default": []any{
										map[string]any{
											"kind":     "no_nulls",
											"severity": "warning",
											"config": map[string]any{
												"field": "id",
											},
										},
										map[string]any{
											"kind":     "numeric_range",
											"severity": "error",
											"config": map[string]any{
												"field": "value",
												"min":   float64(0),
												"max":   float64(100),
											},
										},
									},
								},
								"rows": map[string]any{
									"type":        "array",
									"description": "tabular payload to verify",
									"default":     rowsAny,
								},
							},
							"required": []any{"checks", "rows"},
						},
					},
				},
			},
		},
	}
}

func requireVerifierSucceededWithWarning(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) {
	t.Helper()
	var sawDispatch bool
	obs := ep.PollNodeObservability(t, instanceID, nodeType, func(o harness.NodeObservability) bool {
		sawDispatch = sawDispatch || o.HasEventKind("work_started")
		if sawDispatch && o.RunSummary.FailedCount > 0 {
			t.Fatalf("warning leg: node %q settled with failed_count=%d after a real dispatch — "+
				"a warning-severity failure must NOT block the commit. "+
				"Falsifier hit: Warning blocks commit, OR the severity field is "+
				"declared but unused (both look the same: a warning-only failure "+
				"flipped the terminal).\nlast GET /v1/observability/nodes/%s/%s body:\n%s",
				nodeType, o.RunSummary.FailedCount, instanceID, nodeType, string(o.LatestAttributes))
		}
		return sawDispatch && o.RunSummary.FreshCount > 0
	})
	assertWarningRecorded(t, decodeLatestAttributes(t, obs.LatestAttributes), string(obs.LatestAttributes))
}

func decodeLatestAttributes(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode latest_attributes: %v: %s", err, string(raw))
	}
	return out
}

func assertWarningRecorded(t *testing.T, latest map[string]any, debugBody string) {
	t.Helper()
	if latest == nil {
		t.Fatalf("warning leg: latest_attributes missing — the verifier's success-path "+
			"`attributes_delta` did not surface on the node-read response. "+
			"Falsifier hit: severity field declared but unused (no warning observability).\n"+
			"observability body:\n%s", debugBody)
	}
	rawCount, hasCount := latest["verifier_warning_count"]
	if !hasCount {
		t.Fatalf("warning leg: latest_attributes lacks `verifier_warning_count` — "+
			"the verifier-shape-checks success delta did not surface its warning count. "+
			"Falsifier hit: severity field declared but unused.\n"+
			"latest_attributes:\n%s", debugBody)
	}
	count, ok := rawCount.(float64)
	if !ok {
		t.Fatalf("warning leg: `verifier_warning_count` is not numeric: %T (%v); "+
			"the verifier's success-delta shape changed in a way that hides the warning count\n"+
			"latest_attributes:\n%s", rawCount, rawCount, debugBody)
	}
	if count < 1 {
		t.Fatalf("warning leg: verifier_warning_count=%v, want >= 1 — the no_nulls warning "+
			"was either skipped or not bucketed as a warning. Falsifier hit: severity "+
			"field declared but unused (the warning check never ran or was treated as a "+
			"blocking failure).\nlatest_attributes:\n%s", count, debugBody)
	}
	rawWarnings, hasWarnings := latest["verifier_warnings"]
	if !hasWarnings {
		t.Fatalf("warning leg: verifier_warning_count=%v but `verifier_warnings` is absent — "+
			"the count without the entries makes the warning unobservable to the operator\n"+
			"latest_attributes:\n%s", count, debugBody)
	}
	entries, ok := rawWarnings.([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("warning leg: `verifier_warnings` is not a non-empty array (got %T %v) — "+
			"the warning count is non-zero but the warnings list is empty\n"+
			"latest_attributes:\n%s", rawWarnings, rawWarnings, debugBody)
	}
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if entry["kind"] == "no_nulls" && entry["severity"] == "warning" {
			return
		}
	}
	t.Fatalf("warning leg: `verifier_warnings` lacks the expected no_nulls+warning entry — "+
		"the verifier may be bucketing the failed warning as a blocking failure instead of "+
		"a soft finding\nentries=%v\nlatest_attributes:\n%s", entries, debugBody)
}

func requireVerifierFailedWithCheckFailedClass(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) {
	t.Helper()
	sawDispatch := false
	awaited.Until(t, fmt.Sprintf("node %q on instance %s to take a real dispatch from the bundled "+
		"verifier-shape-checks executor and settle failed", nodeType, instanceID), func() bool {
		status, obs, raw := ep.GetNodeObservability(t, instanceID, nodeType)
		if status != http.StatusOK {
			return false
		}
		sawDispatch = sawDispatch || obs.HasEventKind("work_started")
		if !sawDispatch {
			return false
		}
		if obs.RunSummary.FreshCount > 0 {
			t.Fatalf("error leg: node %q settled with fresh_count=%d after a real dispatch "+
				"despite the error-severity numeric_range check failing — the commit "+
				"was NOT blocked. Falsifier hit: Error doesn't block commit.\n"+
				"last GET /v1/observability/nodes/%s/%s body:\n%s",
				nodeType, obs.RunSummary.FreshCount, instanceID, nodeType, string(raw))
		}
		if obs.RunSummary.FailedCount == 0 {
			return false
		}
		if !anyEventCarriesAnErrorClass(obs.Events) {
			return false
		}
		requireVerifierCheckFailedErrorClass(t, obs.Events, string(raw))
		return true
	})
}

func anyEventCarriesAnErrorClass(events []harness.NodeEvent) bool {
	for _, e := range events {
		if cls, ok := e.Payload["error_class"].(string); ok && cls != "" {
			return true
		}
	}
	return false
}

func requireVerifierCheckFailedErrorClass(t *testing.T, events []harness.NodeEvent, debugBody string) {
	t.Helper()
	const want = "verifier/check_failed/numeric_range"
	for _, e := range events {
		if cls, ok := e.Payload["error_class"].(string); ok {
			if cls == want {
				return
			}
		}
		if strings.Contains(e.Kind, "terminal/error/") && strings.Contains(e.Kind, want) {
			return
		}
	}
	t.Fatalf("error leg: node settled `failed` but no event carries error_class "+
		"`%s` — either the failure landed via an unrelated infra path (executor "+
		"crash, transport error) rather than the bundled verifier's blocking-check "+
		"terminal, or the executor misattributed the blocking class to the "+
		"warning-severity no_nulls check instead of the blocking numeric_range "+
		"check. Falsifier hit: the error-severity partition did not drive the "+
		"terminal class.\nevent log:\n%s\nfull observability body:\n%s",
		want, formatEventsForDiagnostic(events), debugBody)
}

func formatEventsForDiagnostic(events []harness.NodeEvent) string {
	var b strings.Builder
	for i, e := range events {
		fmt.Fprintf(&b, "  [%d] kind=%q payload=%v\n", i, e.Kind, e.Payload)
	}
	return b.String()
}
