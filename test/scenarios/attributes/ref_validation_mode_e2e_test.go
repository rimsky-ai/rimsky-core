// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const constrainedSchema = `{"type":"object","properties":{"count":{"type":"integer","minimum":0}}}`

func startRefModeHarness(t *testing.T, mode node.RefValidationMode) *scenario.Harness {
	t.Helper()
	return scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		ExtraExecutors: map[string]executor.Endpoint{
			"constrained": scenario.StartStubExecutorWithSchema(t, []byte(constrainedSchema)),
		},
		RefValidationMode: mode,
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

func TestAcceptance_RefValidationMode(t *testing.T) {
	t.Run("all: not-provisioned ref rejected with 400 missing-reference", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateAll)

		status, out := postTemplate(t, h, notProvisionedTemplate("refmode-all-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, status,
			"mode all must reject a not-yet-provisioned executor reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs, "validation_errors must name the missing reference")
	})

	t.Run("available: not-provisioned ref succeeds; provisioned-invalid ref still 400s", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateAvailable)

		okStatus, okOut := postTemplate(t, h, notProvisionedTemplate("refmode-avail-ok-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, okStatus,
			"mode available must accept a not-yet-provisioned executor reference; body: %v", okOut)
		require.NotEmpty(t, okOut["template_id"],
			"a successful registration must return a template_id; body: %v", okOut)

		badStatus, badOut := postTemplate(t, h, provisionedInvalidTemplate("refmode-avail-bad-"+uuid.NewString()))
		require.Equal(t, http.StatusBadRequest, badStatus,
			"mode available must still reject a genuinely-invalid provisioned ref; body: %v", badOut)
		errs, ok := badOut["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", badOut)
		require.NotEmpty(t, errs, "validation_errors must name the schema violation")
	})

	t.Run("rejection names the active mode and the config key; the advised relaxed mode accepts", func(t *testing.T) {
		t.Parallel()
		spec := notProvisionedTemplate("refmode-msg-" + uuid.NewString())

		strict := startRefModeHarness(t, node.RefValidateAll)
		status, out := postTemplate(t, strict, spec)
		require.Equal(t, http.StatusBadRequest, status,
			"mode all must reject the unprovisioned reference; body: %v", out)
		errs, ok := out["validation_errors"].([]any)
		require.True(t, ok, "rejection must carry validation_errors; body: %v", out)
		require.NotEmpty(t, errs)
		var msgs []string
		for _, e := range errs {
			entry, entryOK := e.(map[string]any)
			require.True(t, entryOK, "validation_errors entries must be objects; got %v", e)
			msg, msgOK := entry["msg"].(string)
			require.True(t, msgOK, "validation_errors entries must carry msg; got %v", entry)
			msgs = append(msgs, msg)
		}
		joined := strings.Join(msgs, "\n")
		require.Contains(t, joined, `"ghost-executor"`,
			"the rejection must name the failing reference; messages: %s", joined)
		require.Contains(t, joined, `mode "all"`,
			"the rejection must name the active reference-validation mode; messages: %s", joined)
		require.Contains(t, joined, "templates.ref_validation_mode",
			"the rejection must name the config key that changes the behavior; messages: %s", joined)
		require.Contains(t, joined, `"available"`,
			"the rejection must name the relaxed settings for register-first workflows; messages: %s", joined)
		require.Contains(t, joined, `"none"`,
			"the rejection must name the relaxed settings for register-first workflows; messages: %s", joined)

		relaxed := startRefModeHarness(t, node.RefValidateAvailable)
		okStatus, okOut := postTemplate(t, relaxed, spec)
		require.Equal(t, http.StatusCreated, okStatus,
			"the relaxed mode the rejection advises must accept the same template; body: %v", okOut)
		require.NotEmpty(t, okOut["template_id"],
			"a successful registration must return a template_id; body: %v", okOut)
	})

	t.Run("none: no registration-time reference validation", func(t *testing.T) {
		t.Parallel()
		h := startRefModeHarness(t, node.RefValidateNone)

		ghostStatus, ghostOut := postTemplate(t, h, notProvisionedTemplate("refmode-none-ghost-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, ghostStatus,
			"mode none must accept a not-yet-provisioned executor reference; body: %v", ghostOut)
		require.NotEmpty(t, ghostOut["template_id"],
			"a successful registration must return a template_id; body: %v", ghostOut)

		invalidStatus, invalidOut := postTemplate(t, h, provisionedInvalidTemplate("refmode-none-invalid-"+uuid.NewString()))
		require.Equal(t, http.StatusCreated, invalidStatus,
			"mode none must perform no registration-time reference validation; body: %v", invalidOut)
		require.NotEmpty(t, invalidOut["template_id"],
			"a successful registration must return a template_id; body: %v", invalidOut)
	})
}
