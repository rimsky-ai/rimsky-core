// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"testing"

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
