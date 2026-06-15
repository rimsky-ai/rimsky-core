// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof for STORY-verifier-severity-partition: a template
// author labels a verifier check `warning` or `error` and the assembled
// product honors the partition — failing-warning is non-blocking (the
// dispatch settles success and the warning is recorded on the node's
// resolved-attribute surface) while failing-error blocks the commit
// (the dispatch settles failed with a `verifier/check_failed/<kind>`
// error class).
//
// The story names the partition contract in user terms: "label a check
// `warning` or `error` and have the verifier honor the partition". The
// load-bearing surfaces are the bundled `verifier-shape-checks`
// executor's actual runtime severity dispatch + the assembled rimsky
// stack's terminal accounting. Existing artifacts cover in-process
// behavior (`server_test.go` / `validation_test.go` under
// `lib/services/executors/verifier-shape-checks/`); this scenario closes
// the cross-stack leg the spec calls out as needing verification.
//
// The two dispatches drive opposite halves of the partition against the
// REAL bundled image:
//
//  1. In-bounds dataset, two checks declared:
//       - severity=warning, FAILING  (no_nulls on a field that has nulls)
//       - severity=error, PASSING    (numeric_range with the dataset
//         inside the bound)
//     The node must reach terminal SUCCESS (state=fresh) and the
//     warning must be observable on `latest_attributes` —
//     `verifier_warning_count >= 1` with the failed check entry in
//     `verifier_warnings`.
//
//  2. Out-of-bounds dataset against the SAME template wiring:
//       - severity=warning, FAILING
//       - severity=error, FAILING   (numeric_range now violated)
//     The node must reach terminal FAILED (state=failed) and the
//     observability event log must carry a `verifier/check_failed/...`
//     error_class — proving the commit was blocked by the error-severity
//     failure, not by the warning.
//
// The runtime treats severity as `warning` vs non-`warning` (any
// non-`warning` string is treated as blocking — see
// `tension:quality-rule-severity-string-footgun`). This scenario
// exercises only the two known-good values; the typo footgun is out of
// scope here.
//
// Falsifier brief from the story: "Warning blocks commit, OR error
// doesn't block commit, OR the severity field is declared but unused."
// Each is the kind of defect this scenario catches end-to-end:
//   - "Warning blocks commit" → the in-bounds dispatch would settle
//     `failed` instead of `fresh`.
//   - "Error doesn't block commit" → the out-of-bounds dispatch would
//     settle `fresh` instead of `failed`.
//   - "severity field declared but unused" → the in-bounds dispatch
//     would settle `failed` (because the warning would be treated as
//     blocking), the same surface as "warning blocks commit", PLUS the
//     out-of-bounds run's error_class would not appear (because the
//     severity field would never partition).

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

// TestVerifierSeverityPartition drives two dispatches of the SAME
// verifier-shape-checks-backed template against the real all-in-one
// stack: an in-bounds dataset that flips only the warning, and an
// out-of-bounds dataset that flips both. The first must settle
// `fresh` with the warning recorded; the second must settle `failed`
// with the error class on the event log.
func TestVerifierSeverityPartition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: bundled verifier-shape-checks executor must be on the
	// shared network before rimsky boots so rimsky's startup Capabilities
	// handshake against `verifier-shape-checks` resolves; same pattern as
	// onboarding_demo_e2e_test.go — the bundled image (not a stub) is the
	// value-delivering component the story names.
	netName := harness.NewNetwork(ctx, t)
	harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")

	// @deliberate: Postgres backend rather than the SQLite default —
	// this scenario drives TWO sequential deploy → instance → dispatch
	// round-trips against the same rimsky stack, and the SQLite
	// single-writer path has shown non-deterministic dispatch latency
	// on the second instance (the second template's verifier never
	// reaches a terminal in the 120s window). Postgres has no such
	// single-writer bottleneck. The severity-partition contract is
	// backend-agnostic — sibling SQLite coverage lives in
	// `sqlite_all_in_one_test.go` for the broader single-node loop.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", "verifier-shape-checks:9095"),
	)

	// @deliberate: first leg drives the in-bounds dataset — both `rows`
	// (a list with one row missing the `id` field) and `checks` (a
	// no_nulls warning on `id` plus a numeric_range error on `value`)
	// are baked into the template via JSON-schema `default:` values so
	// the dispatch needs no params. The numeric_range bound 0..100 is
	// satisfied by every row's `value`, so the error-severity check
	// passes. The no_nulls warning fails because the second row is
	// missing `id`. The dispatch must SUCCEED end-to-end (state=fresh)
	// and the warning must surface on `latest_attributes`.
	inBoundsTemplate := buildSeverityPartitionTemplate(
		"severity-partition-in-bounds",
		[]map[string]any{
			{"id": "alpha", "value": float64(10)},
			// @deliberate: missing id — triggers no_nulls warning.
			{"value": float64(20)},
			{"id": "gamma", "value": float64(30)},
		},
	)
	inBoundsTID := deploySeverityPartitionTemplate(t, ep, inBoundsTemplate)
	inBoundsIID := createSeverityPartitionInstance(t, ep, inBoundsTID, "ck-severity-in-bounds")
	requireVerifierSucceededWithWarning(t, ep, inBoundsIID, "verifier", 120*time.Second)

	// @deliberate: second leg drives the out-of-bounds dataset against
	// an identically-wired template (separate template-name to keep
	// template-hash uniqueness honest — same wiring, different
	// defaults). The third row's `value` (250) blows past the 0..100
	// bound, so the error-severity check FAILS. The no_nulls warning
	// still fails too. The dispatch must reach the terminal FAILED
	// state with a `verifier/check_failed/...` error_class on the
	// event log — proving the commit was blocked by the
	// error-severity failure, not by the warning (the warning fails
	// identically in both legs; only the error-severity flip changes
	// the terminal).
	outOfBoundsTemplate := buildSeverityPartitionTemplate(
		"severity-partition-out-of-bounds",
		[]map[string]any{
			{"id": "alpha", "value": float64(10)},
			// @deliberate: missing id — triggers no_nulls warning.
			{"value": float64(20)},
			// @deliberate: out of bound — triggers numeric_range error.
			{"id": "gamma", "value": float64(250)},
		},
	)
	outOfBoundsTID := deploySeverityPartitionTemplate(t, ep, outOfBoundsTemplate)
	outOfBoundsIID := createSeverityPartitionInstance(t, ep, outOfBoundsTID, "ck-severity-out-of-bounds")
	requireVerifierFailedWithCheckFailedClass(t, ep, outOfBoundsIID, "verifier", 120*time.Second)
}

// buildSeverityPartitionTemplate constructs the POST /templates body
// for a single-node `verifier-shape-checks`-backed template that pins
// the `rows` and `checks` arrays as JSON-schema defaults. The two
// checks deliberately straddle the partition:
//
//   - no_nulls on `id` with severity=warning  → non-blocking when failed
//   - numeric_range on `value` (0..100) with severity=error → blocking
//
// The same wiring is reused across both legs; only the row payload
// differs (in-bounds vs out-of-bounds), proving the partition fires
// on the checks' severity declaration, not on a structural template
// difference.
func buildSeverityPartitionTemplate(name string, rows []map[string]any) map[string]any {
	// @constraint: JSON encoder must produce the `default: []` shape the
	// schema validator expects (a heterogeneous list of object literals),
	// so the typed []map[string]any is widened to []any first.
	rowsAny := make([]any, len(rows))
	for i, r := range rows {
		rowsAny[i] = r
	}
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
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

// deploySeverityPartitionTemplate POSTs the template body and deploys
// it; returns the template id. Inlined rather than shared with
// sibling-scenario helpers so the two-leg test reads end-to-end in one
// file.
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

// createSeverityPartitionInstance POSTs a new instance and returns its
// id. The verifier node has no params — every input is baked into the
// template via JSON-schema defaults — so `params: {}` is correct.
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
	return resp.InstanceID
}

// severityNodeReadResponse mirrors GET
// /v1/observability/nodes/{instance_id}/{node_type}: the node row, the
// recent event log entries, and the node's most-recent resolved
// attribute bag (the merged base + verifier `attributes_delta`).
type severityNodeReadResponse struct {
	Node struct {
		State string `json:"state"`
	} `json:"node"`
	Events []struct {
		Kind    string         `json:"kind"`
		Payload map[string]any `json:"payload"`
	} `json:"events"`
	LatestAttributes map[string]any `json:"latest_attributes"`
}

// requireVerifierSucceededWithWarning is the warning-leg assertion: the
// node must (a) have dispatched (work_started present), (b) settle
// `fresh` (success terminal — proof the warning did NOT block commit),
// AND (c) surface the failed warning on `latest_attributes` via the
// verifier's `attributes_delta` merge — `verifier_warning_count >= 1`
// with the no_nulls finding present in `verifier_warnings`. The third
// clause is the load-bearing observable: a state-only assertion (just
// `fresh`) would not distinguish "warning was honored" from "severity
// field was silently dropped and the warning check never ran" — the
// Falsifier's third arm. Reading the warning back through the same
// surface the operator uses pins the partition exhibition end-to-end.
func requireVerifierSucceededWithWarning(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState   string
		sawDispatch bool
		lastBody    string
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			lastBody = string(raw)
			var resp severityNodeReadResponse
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && lastState == "fresh" {
					// @constraint: on success terminal the warning MUST be on
					// the resolved attribute bag — the verifier's success-path
					// `attributes_delta` lifts `verifier_warning_count` +
					// `verifier_warnings` into the merged bag.
					assertWarningRecorded(t, resp.LatestAttributes, lastBody)
					return
				}
				if sawDispatch && lastState == "failed" {
					t.Fatalf("warning leg: node %q settled `failed` after a real dispatch — "+
						"a warning-severity failure must NOT block the commit. "+
						"Falsifier hit: Warning blocks commit, OR the severity field is "+
						"declared but unused (both look the same: a warning-only failure "+
						"flipped the terminal).\nlast GET /v1/observability/nodes/%s/%s body:\n%s",
						nodeType, instanceID, nodeType, lastBody)
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("warning leg: node %q on instance %s did not reach a terminal state within %v "+
		"(last state=%q, work_started seen=%v) — the cross-stack severity-partition exhibition "+
		"never got a real dispatch from the bundled verifier-shape-checks executor.\n"+
		"last GET /v1/observability/nodes/%s/%s body:\n%s",
		nodeType, instanceID, deadline, lastState, sawDispatch, instanceID, nodeType, lastBody)
}

// assertWarningRecorded reads the merged latest-attribute bag for the
// verifier's `verifier_warning_count` + `verifier_warnings` keys. The
// in-bounds dataset's no_nulls warning MUST be reflected:
//
//   - verifier_warning_count >= 1
//   - verifier_warnings includes an entry with kind=no_nulls,
//     severity=warning
//
// A `verifier_warning_count` of 0 (or absent / empty
// `verifier_warnings`) is the third Falsifier arm: the severity field
// was declared but unused — the warning check was either skipped or
// silently bucketed alongside passing checks.
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
	// @constraint: JSON-decoded numbers land as float64.
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

// requireVerifierFailedWithCheckFailedClass is the error-leg
// assertion: the node must (a) have dispatched (work_started present),
// (b) settle `failed` (error terminal — proof the error-severity
// failure DID block the commit), AND (c) carry a
// `verifier/check_failed/...` error_class on the event log.
//
// Without (c), a `failed` settle could be explained by an unrelated
// infrastructure failure (the executor crashed, a transport error,
// etc.); the error-class assertion is the load-bearing proof that the
// terminal landed because of the error-severity check, not despite it.
// Asserting on the canonical hierarchical class (the
// `verifier/check_failed/<kind>` shape the executor emits — see
// `concept:signal`) keeps the gate honest if the verifier ever changes
// which leaf it emits, while still flagging a missing error-class
// emission.
func requireVerifierFailedWithCheckFailedClass(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState   string
		sawDispatch bool
		lastBody    string
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			lastBody = string(raw)
			var resp severityNodeReadResponse
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && lastState == "fresh" {
					t.Fatalf("error leg: node %q settled `fresh` after a real dispatch "+
						"despite the error-severity numeric_range check failing — the commit "+
						"was NOT blocked. Falsifier hit: Error doesn't block commit.\n"+
						"last GET /v1/observability/nodes/%s/%s body:\n%s",
						nodeType, instanceID, nodeType, lastBody)
				}
				if sawDispatch && lastState == "failed" {
					// @constraint: on failed terminal the event log MUST
					// carry a `verifier/check_failed/...` leaf — the
					// canonical hierarchical class the bundled
					// verifier-shape-checks executor emits when an
					// error-severity failure blocks the commit.
					requireVerifierCheckFailedErrorClass(t, resp.Events, lastBody)
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("error leg: node %q on instance %s did not reach a terminal state within %v "+
		"(last state=%q, work_started seen=%v) — the cross-stack severity-partition exhibition "+
		"never got a real dispatch from the bundled verifier-shape-checks executor.\n"+
		"last GET /v1/observability/nodes/%s/%s body:\n%s",
		nodeType, instanceID, deadline, lastState, sawDispatch, instanceID, nodeType, lastBody)
}

// requireVerifierCheckFailedErrorClass walks the event log for the
// canonical verifier error-class signal and asserts the leaf is
// `verifier/check_failed/...`. The signal-kind wire form is
// `signal/terminal/error/<class>` (see `concept:signal`); the
// error_class also appears on the payload of related events
// (e.g. KindError). Either surface satisfies the assertion — the
// load-bearing property is that the verifier's hierarchical class is
// what the supervisor recorded, not which event-row carries it.
func requireVerifierCheckFailedErrorClass(t *testing.T, events []struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}, debugBody string) {
	t.Helper()
	const want = "verifier/check_failed/"
	for _, e := range events {
		// @constraint: direct payload form — events that carry an
		// `error_class` field (KindError, runner_error_policy
		// terminal_signals on the work-rejected/error path) name the
		// class verbatim.
		if cls, ok := e.Payload["error_class"].(string); ok {
			if strings.HasPrefix(cls, want) {
				return
			}
		}
		// @constraint: signal-kind form — `signal/terminal/error/<class>`
		// carries the class as the path suffix of the kind itself.
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

// formatEventsForDiagnostic renders the event log as a flat one-line-per-
// event diagnostic. Keeps the failure message readable when the assertion
// trips far enough into the run that the event log has many entries.
func formatEventsForDiagnostic(events []struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}) string {
	var b strings.Builder
	for i, e := range events {
		fmt.Fprintf(&b, "  [%d] kind=%q payload=%v\n", i, e.Kind, e.Payload)
	}
	return b.String()
}
