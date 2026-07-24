// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

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

func lowerJoin(vals ...any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func startStaticGateHarness(t *testing.T) *scenario.Harness {
	t.Helper()
	return scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			"constrained": scenario.StartStubModeExecutorWithSchema(t, []byte(constrainedSchema)),
		},
	})
}

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

func registerDeployStaticGateTemplate(t *testing.T, h *scenario.Harness, name string, count int) string {
	t.Helper()
	status, out := postTemplate(t, h, staticGateTemplate(name, count))
	require.Equal(t, http.StatusCreated, status,
		"register must succeed for a schema-compliant static default; body: %v", out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID, "register must return a template_id; body: %v", out)

	deployStatus, deployOut := postJSON(t, h, "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, "deploy must succeed; body: %v", deployOut)
	return tplID
}

func TestAcceptance_InstantiationStaticConfigGate(t *testing.T) {
	t.Run("rejects: static count:-1 violates the executor schema's minimum:0 at registration", func(t *testing.T) {
		t.Parallel()
		h := startStaticGateHarness(t)

		status, out := postTemplate(t, h, staticGateTemplate("static-gate-bad-"+uuid.NewString(), -1))
		require.Equal(t, http.StatusBadRequest, status,
			"a static default that violates the executor's expected_attributes_schema must be rejected at registration; body: %v", out)

		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
		errText := lowerJoin(errs)
		require.Contains(t, errText, "count",
			"rejection must name the offending attribute `count`; body: %v", out)
	})

	t.Run("admits: a well-formed instance returns 201 and runs to a terminal state", func(t *testing.T) {
		t.Parallel()
		h := startStaticGateHarness(t)

		tplID := registerDeployStaticGateTemplate(t, h, "static-gate-ok-"+uuid.NewString(), 5)

		status, out := postJSON(t, h, "/v1/instances", map[string]any{
			"template":     tplID,
			"instance_key": "ck-ok-" + uuid.NewString(),
			"target_agent": "scenario-default-agent",
		})
		require.Equal(t, http.StatusCreated, status,
			"a schema-compliant static default (count:5 ≥ minimum:0) must instantiate cleanly; body: %v", out)
		instanceIDStr, _ := out["instance_id"].(string)
		require.NotEmpty(t, instanceIDStr, "a successful create must return an instance_id; body: %v", out)

		require.Equal(t, 1, instanceCountForTemplateE2E(t, h, tplID),
			"a well-formed create must persist exactly one instance row")

		instanceID, err := uuid.Parse(instanceIDStr)
		require.NoError(t, err, "instance_id must parse as a UUID")
		// @decision: empty-message-as-root-trigger
		h.PostInstanceMessage(shared.UUID(instanceID), "", nil, "static-gate-wake-"+instanceID.String())
		root := h.FindNode(shared.UUID(instanceID), "root")
		require.NotNil(t, root, "the instance must materialize its root node")
		h.WaitForNodeState(root.ID, cascade.NodeStateFresh)
	})
}
