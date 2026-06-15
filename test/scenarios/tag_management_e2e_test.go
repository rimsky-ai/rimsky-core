// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-tag-management end-to-end against the real assembled stack.
//
// Drives the full tag-curation lifecycle through the real control-API
// (/v1/tags + /v1/instances surfaces) plus the real persistence layer.
// The proof exhibits, in order, every clause of STORY-tag-management's
// Acceptance:
//
//  1. Register two distinct templates (T_alpha and T_beta), distinguished
//     by name so their content hashes differ.
//  2. POST /v1/tags binds a tag to T_alpha's hash. 201.
//  3. POST /v1/instances against the tag (NOT the hash) succeeds; the
//     returned template_hash equals T_alpha's hash — the tag-resolution
//     path is wired into instance creation (not just into the tags read
//     surface).
//  4. PUT /v1/tags/{tag} rebinds the tag to T_beta's hash. 200.
//  5. A SECOND POST /v1/instances against the same tag now returns
//     T_beta's hash (the rebind is picked up by subsequent instance
//     creation — the spec's first falsifier clause).
//  6. The original instance still references T_alpha's hash (the rebind
//     did NOT migrate existing instances — the spec's "without disrupting
//     in-flight instances" clause).
//  7. DELETE /v1/tags/{tag} returns 200 with {"deleted": true}.
//  8. A third POST /v1/instances against the (now-deleted) tag is refused
//     with 404 (the tag is no longer resolvable for new instances — the
//     spec's second falsifier clause).
//
// The proof drives the REAL control-API HTTP routes against the REAL
// in-process tag-resolution path: resolveTagOrHash (in instances.go's
// create handler) consults the persisted tag row, so a rebind committed
// to the tags table is what the next instance create reads — there is
// no cache in the path that could mask a stale resolution. We don't stub
// any read; we observe through the real /v1/instances response body.
//
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

	// @deliberate: Stub the single worker node for both templates so any instance the
	// supervisor drives reaches terminal through the real dispatch path.
	// The proof does not depend on the executor's payload, only on the
	// template_hash returned by /v1/instances — but a stubbed executor
	// ensures the supervisor doesn't error out on dispatch (which would
	// trigger 5xx-paths unrelated to the story).
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "tag-mgmt-done")

	// @constraint: (1) Register two distinct templates. Distinct content (via name)
	// guarantees distinct hashes — the spec's "different template hash"
	// is what the rebind asserts about, so we MUST exhibit two different
	// hashes here. Canonical naming (`project-alpha`/`project-beta`) per
	// decision:project-agnostic.
	alphaHash := registerTagTemplate(t, h, "project-alpha")
	betaHash := registerTagTemplate(t, h, "project-beta")
	require.NotEqual(t, alphaHash, betaHash,
		"two templates with distinct names must hash to distinct content hashes; "+
			"otherwise the rebind assertion below is vacuous")

	// @deliberate: (2) POST /v1/tags binds a new tag to alpha. The tag is a movable
	// alias the operator uses to refer to alpha without naming its hash.
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

	// @constraint: (3) POST /v1/instances against the TAG (not the hash). The response's
	// template_hash must equal alpha's hash — the tag-resolution path is
	// wired through the instance-create handler, not just the read surface.
	alphaInstance := createInstanceAgainstTag(t, h, tagName)
	require.Equal(t, alphaHash, alphaInstance.templateHash,
		"instance created against tag %q must reference alpha's hash %s (got %s) — "+
			"the tag-resolution path must be wired through instance creation",
		tagName, alphaHash, alphaInstance.templateHash)
	require.NotEmpty(t, alphaInstance.instanceID,
		"create response must carry a non-empty instance_id")

	// @constraint: (4) PUT /v1/tags/{tag} rebinds the tag to beta. The handler
	// atomically swaps the persisted row's template_id. There is no
	// transitional state visible to subsequent reads — the next
	// resolveTagOrHash call reads beta or alpha, never both.
	moveResp := putJSON(t, h.ControlBase+"/v1/tags/"+tagName, map[string]any{
		"template": betaHash,
	})
	require.Equal(t, http.StatusOK, moveResp.status,
		"PUT /v1/tags/%s rebinding to beta must return 200: %s",
		tagName, moveResp.bodyStr())
	require.Equal(t, betaHash, moveResp.stringField("template_id"),
		"rebind response must report the new template hash (beta)")

	// @deliberate: (5) A FRESH POST /v1/instances against the same tag now resolves
	// to beta — the spec's first falsifier ("Tag rebind isn't picked up
	// by subsequent instance creation (resolves to the prior hash)").
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

	// @constraint: (6) Re-read the ORIGINAL (alpha) instance. The rebind must NOT
	// migrate it; its template_hash must still be alpha. This is the
	// spec's "without disrupting in-flight instances" clause.
	alphaReread := getJSON(t, h.ControlBase+"/v1/instances/"+alphaInstance.instanceID)
	require.Equal(t, http.StatusOK, alphaReread.status,
		"GET /v1/instances/{alphaInstanceID} must return 200: %s",
		alphaReread.bodyStr())
	require.Equal(t, alphaHash, alphaReread.stringField("template_hash"),
		"the original instance (created pre-rebind) must STILL reference "+
			"alpha's hash %s (got %s) after the tag is repointed to beta — "+
			"the rebind must not migrate existing instances",
		alphaHash, alphaReread.stringField("template_hash"))

	// @constraint: (7) DELETE /v1/tags/{tag} retires the alias. Subsequent reads of
	// the tag (via tag-resolution in instance create) must see it as
	// absent.
	deleteResp := doRequest(t, http.MethodDelete,
		h.ControlBase+"/v1/tags/"+tagName, nil)
	require.Equal(t, http.StatusOK, deleteResp.status,
		"DELETE /v1/tags/%s must return 200: %s", tagName, deleteResp.bodyStr())
	require.Contains(t, deleteResp.bodyStr(), `"deleted":true`,
		"DELETE response must report deleted:true")

	// @constraint: (8) A fresh POST /v1/instances against the now-deleted tag must
	// be refused. resolveTagOrHash returns empty for a missing tag,
	// which the instance-create handler translates to a 404 with the
	// ErrTemplateNotFound sentinel — the spec's second falsifier
	// ("tag deletion leaves the name still resolving").
	postDeleteAttempt := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": tagName, "params": map[string]any{}})
	require.GreaterOrEqual(t, postDeleteAttempt.status, 400,
		"POST /v1/instances against a deleted tag must be refused (4xx) — "+
			"got %d: %s — the spec's `tag deletion leaves the name still "+
			"resolving` falsifier is what this assertion negates",
		postDeleteAttempt.status, postDeleteAttempt.bodyStr())
	require.Less(t, postDeleteAttempt.status, 500,
		"refusal must be a 4xx client error, not a 5xx server error")
}

// instanceCreateResult carries the two fields the tag-management proof
// reads from a /v1/instances POST response: the assigned instance_id and
// the template_hash the create handler resolved the request's `template`
// field to. The spec's falsifier is exactly that the template_hash is
// stale or wrong; reading it explicitly here makes the assertion legible.
type instanceCreateResult struct {
	instanceID   string
	templateHash string
}

// createInstanceAgainstTag POSTs /v1/instances with template=tag (the
// tag-resolution-on-create path). Returns the (instance_id,
// template_hash) pair the server reports. The harness's CreateInstance
// helper resolves the hash up-front and would mask the rebind property
// this test is asserting; we use the raw HTTP path deliberately.
func createInstanceAgainstTag(t *testing.T, h *scenario.Harness, tag string) instanceCreateResult {
	t.Helper()
	resp := postJSON(t, h.ControlBase+"/v1/instances",
		map[string]any{"template": tag, "params": map[string]any{}})
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

// registerTagTemplate registers (does NOT deploy) a one-node template
// distinguished by `name` so its content hash is unique. Returns the
// hash. Used to mint two distinct templates the tag rebind moves
// between. The template is then deployed so /v1/instances will accept
// it (the deployed-state gate is exercised by STORY-template-lifecycle,
// not here — this proof needs both templates deployed).
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

// putJSON issues a PUT with a JSON body. Mirrors postJSON/getJSON
// (defined in template_lifecycle_e2e_test.go); lives here because the
// tag-management proof is the first scenario in this package to issue
// a PUT (PUT /v1/tags/{tag} is the rebind verb) and the shared helper
// file doesn't carry a PUT variant.
func putJSON(t *testing.T, url string, body any) jsonResp {
	t.Helper()
	return doRequest(t, http.MethodPut, url, body)
}
