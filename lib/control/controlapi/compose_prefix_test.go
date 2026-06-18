// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const composeOriginHeaderName = "X-Rimsky-Compose-Origin"

func registerAndDeploy(t *testing.T, h *harness, namePrefix string) string {
	t.Helper()
	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody(namePrefix+"-"+uuid.NewString()))
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID, "register did not return a template_id: %v", out)
	deployStatus, deployOut := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, deployOut)
	return tplID
}

func tagPresent(t *testing.T, h *harness, name string) bool {
	t.Helper()
	status, listed := h.httpJSON(t, "GET", "/v1/tags", nil)
	require.Equal(t, http.StatusOK, status, listed)
	tags, _ := listed["tags"].([]any)
	for _, raw := range tags {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["tag"] == name {
			return true
		}
	}
	return false
}

func TestCreateTag_ComposePrefixRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-tag-reject")

	reservedTag := "compose:my-app:v1"
	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	})

	require.Equal(t, http.StatusBadRequest, status, body)
	errMsg, _ := body["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "reserved",
		"error should name the reserved prefix: %v", body)
	require.Contains(t, errMsg, "compose:",
		"error should cite the compose: prefix: %v", body)

	require.False(t, tagPresent(t, h, reservedTag),
		"reserved-prefix tag was persisted despite the rejection")
}

func TestCreateInstance_ComposePrefixRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-inst-reject")

	reservedKey := "compose:my-app:i1"
	status, body := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": reservedKey,
	})

	require.Equal(t, http.StatusBadRequest, status, body)
	errMsg, _ := body["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "reserved",
		"error should name the reserved prefix: %v", body)
	require.Contains(t, errMsg, "compose:",
		"error should cite the compose: prefix: %v", body)

	getStatus, getBody := h.httpJSON(t, "GET", "/v1/instances/"+reservedKey, nil)
	require.Equal(t, http.StatusNotFound, getStatus, getBody)
}

func TestCreateTag_ComposeOriginAllowed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-tag-allow")

	reservedTag := "compose:my-app:v1"
	resp := h.httpJSONWithHeaders(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	}, map[string]string{composeOriginHeaderName: "1"})

	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	require.Equal(t, reservedTag, resp.body["tag"], resp.body)

	require.True(t, tagPresent(t, h, reservedTag),
		"compose-origin tag create should have persisted the reserved tag")
}
