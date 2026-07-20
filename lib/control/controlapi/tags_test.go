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

func TestCreateTag_RejectsSlash(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("slash-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag": "env/prod-" + uuid.NewString(), "template": tplID,
	})
	require.Equal(t, http.StatusBadRequest, status, body,
		"a tag containing '/' would be unaddressable via the {tag}/{id} path routes once created, so it must be rejected at creation")
}

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

func TestDeleteTag_404OnMissing(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	missingTag := "ghost-" + uuid.NewString()
	status, _ := h.httpJSON(t, "DELETE", "/v1/tags/"+missingTag, nil)
	require.Equal(t, http.StatusNotFound, status)
}

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

	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)
}
