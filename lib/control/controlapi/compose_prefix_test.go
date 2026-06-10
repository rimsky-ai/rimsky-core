// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_prefix_test.go — server-side enforcement of the reserved
// `compose:` tag / instance_key prefix
// (S-control-api-mcp-compose-prefix-server-guard,
// spec:2026-06-06-comprehensive-gap-closure). The CLI reserved the
// prefix client-side only; these tests pin the control-api server as
// the source of truth: any client (raw HTTP, no CLI) attempting to
// create a `compose:`-prefixed tag or instance_key is rejected with
// 400 + a reserved-prefix diagnostic and NOTHING is persisted — UNLESS
// the request carries the trusted compose-origin marker
// (`X-Rimsky-Compose-Origin: 1`), which the real compose engine sets
// because it legitimately owns the prefix.
package controlapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// composeOriginHeaderName is the trusted marker the compose engine
// stamps on its writes so the server guard can tell a compose-origin
// write from a foreign one. Mirrors the constant the GREEN pass adds
// in compose_prefix.go (CLICTRL-4.2). Kept local to the test so the RED
// pass compiles before that file exists.
const composeOriginHeaderName = "X-Rimsky-Compose-Origin"

// registerAndDeploy registers validTemplateBody under a fresh name,
// deploys it, and returns the resolvable template hash. The compose
// prefix guard sits ahead of the template lookup, so the create paths
// under test need a real deployed template to prove the rejection is
// the guard firing (not a missing-template 404).
func registerAndDeploy(t *testing.T, h *harness, namePrefix string) string {
	t.Helper()
	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody(namePrefix+"-"+uuid.NewString()))
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID, "register did not return a template_id: %v", out)
	deployStatus, deployOut := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, deployOut)
	return tplID
}

// tagPresent reports whether GET /tags lists a tag with the given name.
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

// TestCreateTag_ComposePrefixRejected: a raw POST /tags (no CLI, no
// compose-origin marker) naming a `compose:`-prefixed tag must be
// rejected by the server itself with 400 + a reserved-prefix
// diagnostic, and no tag row may be created.
//
// RED today: validTag accepts `:`, the prefix check does not exist, so
// the create succeeds (201) — the status assertion fails.
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

	// Nothing was persisted: the reserved tag must not appear on GET /tags.
	require.False(t, tagPresent(t, h, reservedTag),
		"reserved-prefix tag was persisted despite the rejection")
}

// TestCreateInstance_ComposePrefixRejected: a POST /instances whose
// instance_key uses the reserved `compose:` prefix (no marker) must be
// rejected by the server with 400 + a reserved-prefix diagnostic, and
// no instance may be created (GET /instances/{key} → 404).
//
// RED today: the instance-key path applies no prefix check, so the
// create succeeds (201) — the status assertion fails.
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

	// No instance was created: GET by the reserved instance_key is 404.
	getStatus, getBody := h.httpJSON(t, "GET", "/v1/instances/"+reservedKey, nil)
	require.Equal(t, http.StatusNotFound, getStatus, getBody)
}

// TestCreateTag_ComposeOriginAllowed: the SAME reserved-prefix tag
// create succeeds (201, and the tag appears on GET /tags) when the
// request carries the trusted compose-origin marker — proving the
// guard discriminates compose-originated writes from foreign ones
// rather than blocking the prefix unconditionally.
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
