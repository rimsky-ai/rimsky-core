// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// tags_test.go — coverage for the POST /tags / PUT /tags/{tag} /
// DELETE /tags/{tag} routes added by the 2026-05-01 control-plane
// spec §1.5.
package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestCreateTag_HappyPath: POST /tags with a valid tag and an existing
// template hash creates a tag pointing at the row.
func TestCreateTag_HappyPath(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("tag-hp-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	tag := "newtag-" + uuid.NewString()
	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag": tag, "template": tplID,
	})
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, tag, body["tag"])
	require.Equal(t, tplID, body["template_id"])
}

// TestCreateTag_DuplicateRejected: re-POST a tag that already exists
// returns 409.
func TestCreateTag_DuplicateRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("dup-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	tag := "dup-" + uuid.NewString()
	status, _ := h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": tplID})
	require.Equal(t, http.StatusCreated, status)
	status, _ = h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": tplID})
	require.Equal(t, http.StatusConflict, status)
}

// TestCreateTag_RejectsHashShape pins Issue 6's fix: a tag whose value
// matches the canonical content-hash shape must fail validation.
func TestCreateTag_RejectsHashShape(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("hashy-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	hashShape := "sha256-" + repeatHex("b", 64)
	status, _ := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag": hashShape, "template": tplID,
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestListTags returns the configured tags. Smoke for the list path.
func TestListTags(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "listed-" + uuid.NewString()
	body := templateBodyWithTag("tag-list-"+uuid.NewString(), tag)
	_, _ = h.httpJSON(t, "POST", "/v1/templates", body)

	status, listed := h.httpJSON(t, "GET", "/v1/tags", nil)
	require.Equal(t, http.StatusOK, status, listed)
	tags := listed["tags"].([]any)
	require.NotEmpty(t, tags)
}

// TestMoveTag_404OnMissing: PUT /tags/{tag} on a tag that doesn't exist
// returns 404 (operators can't accidentally create a tag via PUT).
func TestMoveTag_404OnMissing(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("move-target-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	missingTag := "ghost-" + uuid.NewString()
	status, _ := h.httpJSON(t, "PUT", "/v1/tags/"+missingTag, map[string]any{"template": tplID})
	require.Equal(t, http.StatusNotFound, status)
}

// TestDeleteTag_404OnMissing: DELETE /tags/{tag} on a tag that doesn't
// exist returns 404 (operators get a clear signal rather than a silent
// no-op 200).
func TestDeleteTag_404OnMissing(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	missingTag := "ghost-" + uuid.NewString()
	status, _ := h.httpJSON(t, "DELETE", "/v1/tags/"+missingTag, nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestDeleteTag_DoesNotDeleteTemplate verifies that DELETE /tags/{tag}
// removes only the tag row; the template persists. This is the route
// at /tags/{tag}, distinct from the template-delete route at
// /templates/{tag_or_hash} which CAN cascade.
func TestDeleteTag_DoesNotDeleteTemplate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tag := "soft-delete-" + uuid.NewString()
	body := templateBodyWithTag("tag-delete-"+uuid.NewString(), tag)
	_, out := h.httpJSON(t, "POST", "/v1/templates", body)
	tplID := out["template_id"].(string)

	status, _ := h.httpJSON(t, "DELETE", "/v1/tags/"+tag, nil)
	require.Equal(t, http.StatusOK, status)

	// @constraint: template still resolvable by hash.
	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)
}
