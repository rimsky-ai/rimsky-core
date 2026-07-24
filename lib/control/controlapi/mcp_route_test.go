// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message-schema
// @concept: control-api

package controlapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPDELETE_ReachesMountedHandlerNot405(t *testing.T) {
	h := newMCPParityHarness(t)
	bearer := h.mintKey(t, "mcp-delete-caller", `[{"action":"*"}]`)
	sid := h.ensureMCPSession(t, bearer)

	status, _, _ := h.httpWithHeaders(t, http.MethodDelete, "/v1/mcp", bearer, sid, nil)
	require.NotEqual(t, http.StatusMethodNotAllowed, status,
		"DELETE /v1/mcp must reach the mounted MCP handler — a 405 means the router lost the DELETE binding, exactly the falsifier that motivated wiring it explicitly")
}

func TestBuiltinSchemas_MessageSendDescriptorAdvertisesTypeNoRetiredKindOrTarget(t *testing.T) {
	raw, ok := builtinSchemas()["message_send"]
	require.True(t, ok, "message_send must have a builtin schema entry")

	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema must declare properties")

	require.Contains(t, props, "type", "message_send descriptor must advertise `type`")
	require.NotContains(t, props, "kind", "message_send descriptor must not resurrect the retired `kind` field")
	require.NotContains(t, props, "target", "message_send descriptor must not resurrect the retired `target` field")

	required, ok := schema["required"].([]any)
	require.True(t, ok, "schema must declare required fields")
	requiredStrs := make([]string, 0, len(required))
	for _, r := range required {
		s, _ := r.(string)
		requiredStrs = append(requiredStrs, s)
	}
	require.ElementsMatch(t, []string{"id", "type"}, requiredStrs,
		"message_send descriptor's required set must be exactly [id, type]")
}
