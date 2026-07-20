// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestVerifierSeverityPartition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
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
	inBoundsTID := deploySeverityPartitionTemplate(t, ep, inBoundsTemplate)
	inBoundsIID := createSeverityPartitionInstance(t, ep, inBoundsTID, "ck-severity-in-bounds")
	requireVerifierSucceededWithWarning(t, ep, inBoundsIID, "verifier", 120*time.Second)

	outOfBoundsTemplate := buildSeverityPartitionTemplate(
		"severity-partition-out-of-bounds",
		[]map[string]any{
			{"id": "alpha", "value": float64(10)},
			{"value": float64(20)},
			{"id": "gamma", "value": float64(250)},
		},
	)
	outOfBoundsTID := deploySeverityPartitionTemplate(t, ep, outOfBoundsTemplate)
	outOfBoundsIID := createSeverityPartitionInstance(t, ep, outOfBoundsTID, "ck-severity-out-of-bounds")
	requireVerifierFailedWithCheckFailedClass(t, ep, outOfBoundsIID, "verifier", 120*time.Second)
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

func deploySeverityPartitionTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createSeverityPartitionInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "severity-partition", instanceKey)
	return resp.InstanceID
}

func requireVerifierSucceededWithWarning(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	var sawDispatch bool
	obs, ok := ep.PollNodeObservability(t, instanceID, nodeType, deadline, func(o harness.NodeObservability) bool {
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
	if !ok {
		t.Fatalf("warning leg: node %q on instance %s did not reach a terminal state within %v "+
			"(run_summary.fresh_count=%d failed_count=%d, work_started seen=%v) — the cross-stack severity-partition exhibition "+
			"never got a real dispatch from the bundled verifier-shape-checks executor.",
			nodeType, instanceID, deadline, obs.RunSummary.FreshCount, obs.RunSummary.FailedCount, sawDispatch)
	}
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

func requireVerifierFailedWithCheckFailedClass(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastFresh   int
		lastFailed  int
		sawDispatch bool
		lastBody    string
	)
	for time.Now().Before(end) {
		status, obs, raw := ep.GetNodeObservability(t, instanceID, nodeType)
		if status == http.StatusOK {
			lastBody = string(raw)
			lastFresh = obs.RunSummary.FreshCount
			lastFailed = obs.RunSummary.FailedCount
			sawDispatch = sawDispatch || obs.HasEventKind("work_started")
			if sawDispatch && lastFresh > 0 {
				t.Fatalf("error leg: node %q settled with fresh_count=%d after a real dispatch "+
					"despite the error-severity numeric_range check failing — the commit "+
					"was NOT blocked. Falsifier hit: Error doesn't block commit.\n"+
					"last GET /v1/observability/nodes/%s/%s body:\n%s",
					nodeType, lastFresh, instanceID, nodeType, lastBody)
			}
			if sawDispatch && lastFailed > 0 {
				requireVerifierCheckFailedErrorClass(t, obs.Events, lastBody)
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("error leg: node %q on instance %s did not reach a terminal state within %v "+
		"(run_summary.fresh_count=%d failed_count=%d, work_started seen=%v) — the cross-stack severity-partition exhibition "+
		"never got a real dispatch from the bundled verifier-shape-checks executor.\n"+
		"last GET /v1/observability/nodes/%s/%s body:\n%s",
		nodeType, instanceID, deadline, lastFresh, lastFailed, sawDispatch, instanceID, nodeType, lastBody)
}

func requireVerifierCheckFailedErrorClass(t *testing.T, events []harness.NodeEvent, debugBody string) {
	t.Helper()
	const want = "verifier/check_failed/"
	for _, e := range events {
		if cls, ok := e.Payload["error_class"].(string); ok {
			if strings.HasPrefix(cls, want) {
				return
			}
		}
		if strings.Contains(e.Kind, "/terminal/error/") {
			if strings.Contains(e.Kind, want) {
				return
			}
		}
	}
	t.Fatalf("error leg: node settled `failed` but no event carries a "+
		"`%s<kind>` error_class — the failure may have landed via an "+
		"unrelated infra path (executor crash, transport error) rather "+
		"than the bundled verifier's blocking-check terminal. Falsifier hit: "+
		"the error-severity partition did not drive the terminal class.\n"+
		"event log:\n%s\nfull observability body:\n%s",
		want, formatEventsForDiagnostic(events), debugBody)
}

func formatEventsForDiagnostic(events []harness.NodeEvent) string {
	var b strings.Builder
	for i, e := range events {
		fmt.Fprintf(&b, "  [%d] kind=%q payload=%v\n", i, e.Kind, e.Payload)
	}
	return b.String()
}
