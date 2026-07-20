// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestApplyParamsRedact_TopLevel(t *testing.T) {
	params := map[string]any{"token": "secret", "name": "ok"}
	out := ApplyParamsRedact(params, []string{"token"})
	require.Equal(t, "[REDACTED]", out["token"])
	require.Equal(t, "ok", out["name"])
	require.Equal(t, "secret", params["token"], "the input map must not be mutated")
}

func TestApplyParamsRedact_DottedNestedPath(t *testing.T) {
	params := map[string]any{
		"credentials": map[string]any{
			"token":   "secret-value",
			"account": "visible",
		},
		"name": "ok",
	}
	out := ApplyParamsRedact(params, []string{"credentials.token"})

	creds, ok := out["credentials"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", creds["token"])
	require.Equal(t, "visible", creds["account"])
	require.Equal(t, "ok", out["name"])

	origCreds := params["credentials"].(map[string]any)
	require.Equal(t, "secret-value", origCreds["token"], "the input map must not be mutated")
}

func TestApplyParamsRedact_DeeplyNestedPath(t *testing.T) {
	params := map[string]any{
		"db": map[string]any{
			"replica": map[string]any{
				"password": "secret",
			},
		},
	}
	out := ApplyParamsRedact(params, []string{"db.replica.password"})
	db := out["db"].(map[string]any)
	replica := db["replica"].(map[string]any)
	require.Equal(t, "[REDACTED]", replica["password"])
}

func TestApplyParamsRedact_UnknownPathIsNoOp(t *testing.T) {
	params := map[string]any{"name": "ok"}
	out := ApplyParamsRedact(params, []string{"credentials.token", "missing"})
	require.Equal(t, "ok", out["name"])
	require.NotContains(t, out, "credentials")
	require.NotContains(t, out, "missing")
}

func TestApplyParamsRedact_NonMapParentIsNoOp(t *testing.T) {
	params := map[string]any{"name": "ok"}
	require.NotPanics(t, func() {
		out := ApplyParamsRedact(params, []string{"name.token"})
		require.Equal(t, "ok", out["name"])
	})
}

func TestApplyParamsRedact_EmptyRedactReturnsParamsUnmodified(t *testing.T) {
	params := map[string]any{"token": "secret"}
	out := ApplyParamsRedact(params, nil)
	require.Equal(t, "secret", out["token"])
}

func TestApplyParamsRedact_SentinelRedactsAllTopLevelKeys(t *testing.T) {
	params := map[string]any{
		"token": "secret",
		"nested": map[string]any{
			"inner": "also-secret",
		},
	}
	out := ApplyParamsRedact(params, []string{RedactAllParamsSentinel})
	require.Equal(t, "[REDACTED]", out["token"])
	require.Equal(t, "[REDACTED]", out["nested"],
		"the fail-closed sentinel must redact whole top-level subtrees, not just scalar leaves")
}

func TestInstanceGet_RendersRedactedParamAsRedactedString(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"spec": map[string]any{
			"name":          "redact-http-" + uuid.NewString(),
			"version":       "v1",
			"params_redact": []string{"api_token"},
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
			},
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID := out["template_id"].(string)
	status, _ = h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, createOut := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"params": map[string]any{
			"api_token": "super-secret-value",
			"visible":   "plain-value",
		},
	})
	require.Equal(t, http.StatusCreated, status, createOut)
	instID := createOut["instance_id"].(string)

	status, getOut := h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, getOut)
	params, ok := getOut["params"].(map[string]any)
	require.True(t, ok, "expected a params object in the GET response: %+v", getOut)
	require.Equal(t, "[REDACTED]", params["api_token"],
		"a param declared in the template's params_redact must render as [REDACTED] over HTTP")
	require.Equal(t, "plain-value", params["visible"],
		"a param not declared in params_redact must render unredacted")

	status, listOut := h.httpJSON(t, "GET", "/v1/instances", nil)
	require.Equal(t, http.StatusOK, status, listOut)
	instances, _ := listOut["instances"].([]any)
	found := false
	for _, item := range instances {
		row, _ := item.(map[string]any)
		if row["id"] != instID {
			continue
		}
		found = true
		listParams, _ := row["params"].(map[string]any)
		require.Equal(t, "[REDACTED]", listParams["api_token"],
			"the list endpoint must apply the same params_redact as the get endpoint")
	}
	require.True(t, found, "created instance must appear in the list")
}
