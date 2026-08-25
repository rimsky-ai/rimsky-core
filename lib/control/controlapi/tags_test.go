// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @concept: tag
func TestCreateTag_RepeatingTheSameMappingSucceedsAndPointingElsewhereConflicts(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("dup-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)
	_, other := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("dup-tag-other-"+uuid.NewString()))
	otherTplID := other["template_id"].(string)
	require.NotEqual(t, tplID, otherTplID)

	tag := "dup-" + uuid.NewString()
	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": tplID})
	require.Equal(t, http.StatusCreated, status, body)

	status, body = h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": tplID})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, tag, body["tag"])
	require.Equal(t, tplID, body["template_id"])

	status, body = h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": otherTplID})
	require.Equal(t, http.StatusConflict, status, body)
	require.Equal(t, tplID, body["template_id"])

	status, listed := h.httpJSON(t, "GET", "/v1/tags?limit=500", nil)
	require.Equal(t, http.StatusOK, status, listed)
	found := false
	for _, raw := range listed["tags"].([]any) {
		row := raw.(map[string]any)
		if row["tag"] == tag {
			found = true
			require.Equal(t, tplID, row["template_id"])
		}
	}
	require.True(t, found, "the tag survives a refused re-point: %v", listed)
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

func TestCreateTag_RejectsHashShape_UppercaseHex(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("hashy-upper-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	hashShape := "sha256-" + repeatHex("B", 64)
	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag": hashShape, "template": tplID,
	})
	require.Equal(t, http.StatusBadRequest, status, body,
		"an uppercase-hex hash-shaped string must be rejected as a tag identifier same as lowercase")
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

func TestMoveTag_HappyPath_RepointsTag(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, oldOut := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("move-old-"+uuid.NewString()))
	oldTplID := oldOut["template_id"].(string)
	_, newOut := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("move-new-"+uuid.NewString()))
	newTplID := newOut["template_id"].(string)

	tag := "movable-" + uuid.NewString()
	status, createOut := h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": oldTplID})
	require.Equal(t, http.StatusCreated, status, createOut)
	require.Equal(t, oldTplID, createOut["template_id"])

	status, moveOut := h.httpJSON(t, "PUT", "/v1/tags/"+tag, map[string]any{"template": newTplID})
	require.Equal(t, http.StatusOK, status, moveOut)
	require.Equal(t, tag, moveOut["tag"])
	require.Equal(t, newTplID, moveOut["template_id"], "move must repoint the tag to the new template")
	require.NotEqual(t, oldTplID, moveOut["template_id"])

	status, listed := h.httpJSON(t, "GET", "/v1/tags", nil)
	require.Equal(t, http.StatusOK, status, listed)
	tags, _ := listed["tags"].([]any)
	found := false
	for _, tg := range tags {
		row, _ := tg.(map[string]any)
		if row["tag"] != tag {
			continue
		}
		found = true
		require.Equal(t, newTplID, row["template_id"],
			"the moved tag must resolve to the new template on subsequent reads, not the original")
	}
	require.True(t, found, "moved tag must still be listed")
}

// @concept: tag
func TestCreateTag_ReservesNoPrefix(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody("prefixed-tag-"+uuid.NewString()))
	tplID := out["template_id"].(string)

	for _, tag := range []string{"compose:env-" + uuid.NewString(), "project-alpha:env-" + uuid.NewString()} {
		status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{"tag": tag, "template": tplID})
		require.Equalf(t, http.StatusCreated, status,
			"the server reserves no tag prefix, so any caller may name a tag under any prefix; tag %q got %v", tag, body)
	}
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
