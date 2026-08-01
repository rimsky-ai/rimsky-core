// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attributes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

func startReferenceValidationHarness(t *testing.T) *scenario.Harness {
	t.Helper()
	return scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		ExtraExecutors: map[string]executor.Endpoint{
			"constrained": scenario.StartStubExecutorWithSchema(t, []byte(constrainedSchema)),
		},
	})
}

func postTemplate(t *testing.T, h *scenario.Harness, specBody map[string]any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"spec": specBody})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/templates", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

func notProvisionedTemplate(name string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "v1",
		"nodes": []map[string]any{
			{"type": "root", "executor": "ghost-executor"},
		},
	}
}

func provisionedInvalidTemplate(name string) map[string]any {
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
								"default": -1,
							},
						},
					},
				},
			},
		},
	}
}

func TestAcceptance_ReferenceValidationIsUnconditionallyStrict(t *testing.T) {
	t.Run("not-provisioned executor reference always rejected with 400", func(t *testing.T) {
		t.Parallel()
		h := startReferenceValidationHarness(t)

		status, out := postTemplate(t, h, notProvisionedTemplate("refcheck-notprovisioned-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, status,
			"a not-yet-provisioned executor reference must always be rejected; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the missing reference")
	})

	t.Run("provisioned but schema-invalid reference always rejected with 400", func(t *testing.T) {
		t.Parallel()
		h := startReferenceValidationHarness(t)

		status, out := postTemplate(t, h, provisionedInvalidTemplate("refcheck-invalid-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, status,
			"a genuinely-invalid provisioned reference must always be rejected; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
	})
}
