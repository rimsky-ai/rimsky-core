// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end acceptance gate for the mandatory instantiation-time
// static-config validation gate (story
// S-template-validation-instantiation-mandatory, plan TEMPLCASCADE-AG4).
//
// This boots the REAL assembled product — the in-process control-api
// reachable over HTTP, the real scheduler + supervisor driving dispatch,
// all backed by a testcontainers Postgres, fronting a real observability
// handshake against a real stub-executor gRPC service that advertises a
// CONSTRAINING schema (`count` with `minimum: 0`). It drives the real
// value path the operator observes, not a handler in isolation:
//
//   - The instance-create handler runs its mandatory static-config gate
//     against the executor's live-discovered schema and REJECTS a node
//     whose statically-knowable default (`count: -1`) violates the
//     executor's `minimum: 0` value constraint — HTTP 400 with a
//     validation error that names the offending attribute (`count`) AND
//     cites the `minimum` violation (a genuine value check, not a
//     surface-shape error). The rejected instance is NOT persisted.
//   - A well-formed instance of the SAME template (default `count: 5`)
//     returns 201 and runs to a terminal Complete state through the real
//     scheduler/supervisor dispatch path against the real (stub-mode)
//     constrained executor.
//
// The gate is mandatory regardless of the registration-time reference-
// validation mode: the template is registered under mode `none`
// (registration-time validation off), so the misconfiguration slips past
// registration and MUST be caught at instantiation, where the template is
// deployed and the referenced executor exists + has handshaked.
//
// This is the END-TO-END twin of the per-handler proof in
// lib/control/controlapi/instances_test.go::
// TestCreateInstance_StaticConfigValidationGate. That test pins the
// 400/201 + persistence outcomes at the handler altitude (no run driven);
// this gate stands up the full assembled stack and additionally drives
// the well-formed instance to a terminal verdict — the user-outcome story
// "a well-formed instance returns 201 and runs to a terminal state".
package attributes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// postJSON issues a raw POST against the real control-api with body
// marshalled verbatim (NOT wrapped in `{spec:...}` the way postTemplate
// is) and returns the HTTP status plus the decoded JSON body. Used for
// the /deploy and /instances calls whose bodies are un-wrapped, and which
// must be able to observe a non-2xx (the gate's 400) rather than fataling.
func postJSON(t *testing.T, h *scenario.Harness, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+path, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

// instanceCountForTemplateE2E reads GET /instances filtered to the given
// template hash and returns how many instance rows are persisted for it.
// Proves the no-partial-write property: a rejected create leaves zero
// rows; a well-formed create leaves exactly one.
func instanceCountForTemplateE2E(t *testing.T, h *scenario.Harness, templateHash string) int {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/v1/instances?template_hash=" + templateHash)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /instances must succeed")
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	rows, _ := out["instances"].([]any)
	return len(rows)
}

// lowerJoin renders the given values to a single lowercased string for
// substring assertions over a 400's {error, validation_errors} body.
func lowerJoin(vals ...any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// startStaticGateHarness boots a full real stack (control-api + scheduler
// + supervisor) under registration mode `none` with a constraint-
// advertising "constrained" executor wired as an ExtraExecutors entry.
// The executor runs in immediate-success stub mode so a node referencing
// it actually settles to a terminal Complete verdict — required for the
// well-formed-instance leg, which must RUN, not merely persist. The
// control-api's real observability handshake probes the executor at
// startup so the instantiation gate reads its `minimum: 0` schema from
// the live discovery cache (no fake stands in for the schema source).
func startStaticGateHarness(t *testing.T) *scenario.Harness {
	t.Helper()
	return scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			// @deliberate: Reuse constrainedSchema (declared in ref_validation_mode_e2e_
			// _test.go): {"type":"object","properties":{"count":{"type":
			// "integer","minimum":0}}}. Stub-mode so dispatch settles to
			// terminal success — letting the well-formed leg run to a
			// terminal state through the real supervisor.
			"constrained": scenario.StartStubModeExecutorWithSchema(t, []byte(constrainedSchema)),
		},
		// @constraint: Registration mode `none`: registration-time reference validation
		// is OFF, so a static-default violation (count:-1 vs minimum:0)
		// slips past registration and MUST be caught at instantiation.
		RefValidationMode: node.RefValidateNone,
	})
}

// staticGateTemplate returns a single-node template whose node references
// the PROVISIONED "constrained" executor and whose node default sets
// `count` to the supplied value. With count below the executor schema's
// `minimum: 0` (e.g. -1) it is a genuine static-config violation; with a
// value ≥ 0 it is well-formed.
func staticGateTemplate(name string, count int) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "v1",
		"nodes": []map[string]any{
			{
				"type":     "root",
				"executor": "constrained",
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"count": map[string]any{
								"type":    "integer",
								"default": count,
							},
						},
					},
				},
			},
		},
	}
}

// registerDeployStaticGateTemplate registers + deploys staticGateTemplate
// under the harness's ref-validation mode (`none`), returning the template
// hash. Both register and deploy must succeed — under mode `none` the
// register skips the executor-schema cross-check entirely, so even the
// count:-1 violation registers + deploys clean and the gate is left to
// instantiation. postTemplate (declared in ref_validation_mode_e2e_test.go)
// issues the raw POST /templates and decodes the body.
func registerDeployStaticGateTemplate(t *testing.T, h *scenario.Harness, name string, count int) string {
	t.Helper()
	status, out := postTemplate(t, h, staticGateTemplate(name, count))
	require.Equal(t, http.StatusCreated, status,
		"register must succeed under mode none even for the misconfigured default; body: %v", out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID, "register must return a template_id; body: %v", out)

	deployStatus, deployOut := postJSON(t, h, "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, "deploy must succeed; body: %v", deployOut)
	return tplID
}

// TestAcceptance_InstantiationStaticConfigGate is the end-to-end
// acceptance gate. It drives POST /instances through the real assembled
// stack and asserts the user-observable outcomes: a static-config
// violation is rejected at create-time (400, named attribute + minimum
// violation, nothing persisted), and a well-formed instance of the same
// template is created (201) and runs to a terminal state.
func TestAcceptance_InstantiationStaticConfigGate(t *testing.T) {
	t.Run("rejects: static count:-1 violates the executor schema's minimum:0", func(t *testing.T) {
		t.Parallel()
		h := startStaticGateHarness(t)

		// @constraint: Registered under mode `none` → the violation slips past
		// registration; instantiation is the gate that must catch it.
		tplID := registerDeployStaticGateTemplate(t, h, "static-gate-bad-"+uuid.NewString(), -1)

		status, out := postJSON(t, h, "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-bad-" + uuid.NewString(),
		})
		require.Equal(t, http.StatusBadRequest, status,
			"instantiation must reject a static-config violation at create-time; body: %v", out)

		// @constraint: The rejection must NAME the offending attribute and CITE the
		// `minimum` value-constraint violation — a genuine value check, not
		// a missing/extra-attribute surface error.
		errText := lowerJoin(out["error"], out["validation_errors"])
		require.Contains(t, errText, "count",
			"rejection must name the offending attribute `count`; body: %v", out)
		require.Contains(t, errText, "minimum",
			"rejection must cite the `minimum` value-constraint violation; body: %v", out)

		// @constraint: Nothing was persisted: GET /instances filtered to this template
		// shows zero rows (the no-partial-write property the gate holds).
		require.Equal(t, 0, instanceCountForTemplateE2E(t, h, tplID),
			"a rejected static-config create must persist no instance row")
	})

	t.Run("admits: a well-formed instance returns 201 and runs to a terminal state", func(t *testing.T) {
		t.Parallel()
		h := startStaticGateHarness(t)

		tplID := registerDeployStaticGateTemplate(t, h, "static-gate-ok-"+uuid.NewString(), 5)

		status, out := postJSON(t, h, "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-ok-" + uuid.NewString(),
		})
		require.Equal(t, http.StatusCreated, status,
			"a schema-compliant static default (count:5 ≥ minimum:0) must instantiate cleanly; body: %v", out)
		instanceIDStr, _ := out["instance_id"].(string)
		require.NotEmpty(t, instanceIDStr, "a successful create must return an instance_id; body: %v", out)

		// @constraint: Exactly one instance row persisted for this template.
		require.Equal(t, 1, instanceCountForTemplateE2E(t, h, tplID),
			"a well-formed create must persist exactly one instance row")

		// @deliberate: And it RUNS: the real scheduler/supervisor dispatch the root node
		// to the (stub-mode) constrained executor, which settles it to a
		// terminal Complete verdict. WaitForNodeState(Fresh) returns true
		// only once the node has settled and a terminal/success signal event
		// was appended.
		instanceID, err := uuid.Parse(instanceIDStr)
		require.NoError(t, err, "instance_id must parse as a UUID")
		root := h.FindNode(instanceID, "root")
		require.NotNil(t, root, "the instance must materialize its root node")
		require.True(t, h.WaitForNodeState(root.ID, cascade.NodeStateFresh, 20*time.Second),
			"the well-formed instance's root node must run to a terminal Complete state")
	})
}
