// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message-schema

package controlapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTemplateRegister_RejectsPublisherWithUndeclaredMessageType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("pub-undeclared-" + uuid.NewString())
	spec := specOf(body)
	spec["publishers"] = []map[string]any{
		{
			"name":         "pub-1",
			"kind":         "http",
			"config":       map[string]any{},
			"message_type": "not-a-declared-message-type",
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	errText := fmt.Sprint(out["validation_errors"])
	require.Contains(t, errText, "not-a-declared-message-type")
	require.Contains(t, errText, "messages")
}

func TestTemplateRegister_AcceptsPublisherWithDeclaredMessageType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("pub-declared-" + uuid.NewString())
	spec := specOf(body)
	spec["publishers"] = []map[string]any{
		{
			"name":         "pub-1",
			"kind":         "http",
			"config":       map[string]any{},
			"message_type": "system/invalidate",
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
}
