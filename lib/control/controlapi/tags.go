// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var tagPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@-]{0,254}$`)

func validTag(s string) bool {
	if !tagPattern.MatchString(s) {
		return false
	}
	if looksLikeHash(s) {
		return false
	}
	return true
}

type createTagRequest struct {
	Tag      string `json:"tag"`
	Template string `json:"template"`
}

type moveTagRequest struct {
	Template string `json:"template"`
}

type tagItem struct {
	Tag        string `json:"tag"`
	TemplateID string `json:"template_id"`
	UpdatedAt  string `json:"updated_at"`
}

// @concept: tag
func registerTagsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/tags", gate(deps, "tag:create", handleCreateTag(deps)))
	r.Get("/tags", gate(deps, "tag:read", handleListTags(deps)))
	r.Put("/tags/{tag}", gate(deps, "tag:set", handleMoveTag(deps)))
	r.Delete("/tags/{tag}", gate(deps, "tag:delete", handleDeleteTag(deps)))
}

func handleCreateTag(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body createTagRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if !validTag(body.Tag) {
			badRequest(w, "invalid tag identifier")
			return
		}
		if strings.HasPrefix(body.Tag, composeReservedPrefix) && !isComposeOrigin(req) {
			badRequest(w, "tag uses reserved prefix \"compose:\" (managed by the compose command)")
			return
		}
		hash, err := resolveTagOrHash(req.Context(), deps, body.Template)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		var existing *persistence.TemplateTagRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.TemplateTags().Get(ctx, body.Tag, tx)
			existing = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if existing != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "tag already exists"})
			return
		}
		if WriteDryRunResponse(w, req, "would_have_created_tag", map[string]any{
			"tag":         body.Tag,
			"template_id": hash,
		}) {
			return
		}
		var inserted bool
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			ok, err := deps.Persist.TemplateTags().InsertIfAbsent(ctx, body.Tag, hash, tx)
			inserted = ok
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if !inserted {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "tag already exists"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"tag":         body.Tag,
			"template_id": hash,
		})
	}
}

func handleListTags(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var page persistence.PaginatedListResult[persistence.TemplateTagRow]
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.TemplateTags().List(
				ctx,
				persistence.ListPagination{
					Limit:  parseLimit(req, 100),
					Cursor: req.URL.Query().Get("cursor"),
				},
				tx,
			)
			page = p
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		items := make([]tagItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, tagItem{
				Tag:        r.Tag,
				TemplateID: r.TemplateID,
				UpdatedAt:  r.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tags":        items,
			"next_cursor": page.NextCursor,
		})
	}
}

func handleMoveTag(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		tag := chi.URLParam(req, "tag")
		var existing *persistence.TemplateTagRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.TemplateTags().Get(ctx, tag, tx)
			existing = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if existing == nil {
			notFoundResp(w, "tag not found")
			return
		}
		var body moveTagRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		hash, err := resolveTagOrHash(req.Context(), deps, body.Template)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		if WriteDryRunResponse(w, req, "would_have_moved_tag", map[string]any{
			"tag":          tag,
			"old_template": existing.TemplateID,
			"new_template": hash,
		}) {
			return
		}
		var updated bool
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			ok, err := deps.Persist.TemplateTags().UpdateIfExists(ctx, tag, hash, tx)
			updated = ok
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if !updated {
			notFoundResp(w, "tag not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tag":         tag,
			"template_id": hash,
		})
	}
}

func handleDeleteTag(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		tag := chi.URLParam(req, "tag")
		var existing *persistence.TemplateTagRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.TemplateTags().Get(ctx, tag, tx)
			existing = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if existing == nil {
			notFoundResp(w, "tag not found")
			return
		}
		if WriteDryRunResponse(w, req, "would_have_deleted_tag", map[string]any{
			"tag": tag,
		}) {
			return
		}
		var deleted bool
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			ok, err := deps.Persist.TemplateTags().Delete(ctx, tag, tx)
			deleted = ok
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if !deleted {
			notFoundResp(w, "tag not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}
