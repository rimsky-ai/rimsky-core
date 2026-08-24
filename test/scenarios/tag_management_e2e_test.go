// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: tag-management
package scenarios

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTagManagement_RebindAndDeleteEndToEnd(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "tag-mgmt-done")

	alphaHash := registerTagTemplate(t, h, "project-alpha")
	betaHash := registerTagTemplate(t, h, "project-beta")
	require.NotEqual(t, alphaHash, betaHash,
		"two templates with distinct names must hash to distinct content hashes; "+
			"otherwise the rebind assertion below is vacuous")

	const tagName = "tag-management-proof"
	createResp := postJSON(t, h.ControlBase+"/v1/tags", map[string]any{
		"tag":      tagName,
		"template": alphaHash,
	})
	require.Equal(t, http.StatusCreated, createResp.status,
		"POST /v1/tags binding %s → %s must return 201: %s",
		tagName, alphaHash, createResp.bodyStr())
	require.Equal(t, alphaHash, createResp.stringField("template_id"),
		"create response must report the bound template hash (alpha)")

	alphaInstance := createInstanceAgainstTag(t, h, tagName)
	require.Equal(t, alphaHash, alphaInstance.templateHash,
		"instance created against tag %q must reference alpha's hash %s (got %s) — "+
			"the tag-resolution path must be wired through instance creation",
		tagName, alphaHash, alphaInstance.templateHash)
	require.NotEmpty(t, alphaInstance.instanceID,
		"create response must carry a non-empty instance_id")

	moveResp := putJSON(t, h.ControlBase+"/v1/tags/"+tagName, map[string]any{
		"template": betaHash,
	})
	require.Equal(t, http.StatusOK, moveResp.status,
		"PUT /v1/tags/%s rebinding to beta must return 200: %s",
		tagName, moveResp.bodyStr())
	require.Equal(t, betaHash, moveResp.stringField("template_id"),
		"rebind response must report the new template hash (beta)")

	betaInstance := createInstanceAgainstTag(t, h, tagName)
	require.Equal(t, betaHash, betaInstance.templateHash,
		"after rebind, a fresh instance against tag %q must reference beta's "+
			"hash %s (got %s) — the spec's `Tag rebind isn't picked up by "+
			"subsequent instance creation (resolves to the prior hash)` "+
			"falsifier is what this assertion negates",
		tagName, betaHash, betaInstance.templateHash)
	require.NotEqual(t, alphaInstance.instanceID, betaInstance.instanceID,
		"the two instances created across the rebind must be distinct rows; "+
			"otherwise we're observing an idempotent reuse (same instance_key) "+
			"rather than the rebind property")

	alphaReread := getJSON(t, h.ControlBase+"/v1/instances/"+alphaInstance.instanceID)
	require.Equal(t, http.StatusOK, alphaReread.status,
		"GET /v1/instances/{alphaInstanceID} must return 200: %s",
		alphaReread.bodyStr())
	require.Equal(t, alphaHash, alphaReread.stringField("template_hash"),
		"the original instance (created pre-rebind) must STILL reference "+
			"alpha's hash %s (got %s) after the tag is repointed to beta — "+
			"the rebind must not migrate existing instances",
		alphaHash, alphaReread.stringField("template_hash"))

	deleteResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/tags/"+tagName, nil)
	require.Equal(t, http.StatusOK, deleteResp.status,
		"DELETE /v1/tags/%s must return 200: %s", tagName, deleteResp.bodyStr())
	require.Contains(t, deleteResp.bodyStr(), `"deleted":true`,
		"DELETE response must report deleted:true")

	postDeleteAttempt := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": tagName, "params": map[string]any{}, "target_daemon": "scenario-default-daemon"})
	require.GreaterOrEqual(t, postDeleteAttempt.status, 400,
		"POST /v1/instances against a deleted tag must be refused (4xx) — "+
			"got %d: %s — the spec's `tag deletion leaves the name still "+
			"resolving` falsifier is what this assertion negates",
		postDeleteAttempt.status, postDeleteAttempt.bodyStr())
	require.Less(t, postDeleteAttempt.status, 500,
		"refusal must be a 4xx client error, not a 5xx server error")
}

type instanceCreateResult struct {
	instanceID   string
	templateHash string
}

func createInstanceAgainstTag(t *testing.T, h *scenario.Harness, tag string) instanceCreateResult {
	t.Helper()
	resp := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": tag, "params": map[string]any{}, "target_daemon": "scenario-default-daemon"})
	require.Equal(t, http.StatusCreated, resp.status,
		"POST /v1/instances against tag %q must return 201: %s",
		tag, resp.bodyStr())
	hash := resp.stringField("template_hash")
	require.True(t, strings.HasPrefix(hash, "sha256-"),
		"instance create response must carry a content-hash template_hash; "+
			"got %q", hash)
	return instanceCreateResult{
		instanceID:   resp.stringField("instance_id"),
		templateHash: hash,
	}
}

func registerTagTemplate(t *testing.T, h *scenario.Harness, name string) string {
	t.Helper()
	specBody := buildLifecycleTemplateSpec(t, name, "1")
	regResp := postJSON(t, h.ControlBase+"/v1/templates",
		map[string]any{"spec": specBody})
	require.Equal(t, http.StatusCreated, regResp.status,
		"POST /v1/templates for %s must return 201: %s",
		name, regResp.bodyStr())
	hash := regResp.stringField("template_id")
	require.True(t, strings.HasPrefix(hash, "sha256-"),
		"register response for %s must carry a content hash; got %q",
		name, hash)

	deployResp := postJSON(t, h.ControlBase+"/v1/templates/"+hash+"/deploy",
		map[string]any{})
	require.Equal(t, http.StatusOK, deployResp.status,
		"POST /v1/templates/%s/deploy must return 200: %s",
		hash, deployResp.bodyStr())
	return hash
}

func putJSON(t *testing.T, url string, body any) jsonResp {
	t.Helper()
	return doRequest(t, http.MethodPut, url, body)
}
