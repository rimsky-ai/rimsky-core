// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// tags.go — POST /tags, GET /tags, PUT /tags/{tag}, DELETE /tags/{tag}.
// Per docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md
// §1.5: tags are movable aliases pointing at template content hashes.
package controlapi

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// tagPattern is the canonical tag identifier shape per spec §1.1.
// Disallows hash-shape (sha256-… would fail the first-char class).
var tagPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)

// validTag reports whether s is a syntactically valid tag identifier.
// Per spec §1.1 the tag namespace is disjoint from the content-hash
// namespace; explicitly reject hash-shaped strings so a tag can never
// shadow (or be confused with) a hash on /templates/{id} routes.
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
	Template string `json:"template"` // tag or hash
}

type moveTagRequest struct {
	Template string `json:"template"`
}

type tagItem struct {
	Tag        string `json:"tag"`
	TemplateID string `json:"template_id"`
	UpdatedAt  string `json:"updated_at"`
}

func registerTagsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/tags", handleCreateTag(deps))
	r.Get("/tags", handleListTags(deps))
	r.Put("/tags/{tag}", handleMoveTag(deps))
	r.Delete("/tags/{tag}", handleDeleteTag(deps))
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
		hash, err := resolveTagOrHash(req.Context(), deps, body.Template)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		// Reject when the tag already exists. Use Get to distinguish a
		// pre-existing tag from a missing one; Upsert would silently
		// overwrite, which the POST endpoint does not allow.
		existing, err := deps.Persist.TemplateTags().Get(req.Context(), body.Tag, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if existing != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "tag already exists"})
			return
		}
		if err := deps.Persist.TemplateTags().Upsert(req.Context(), body.Tag, hash, nil); err != nil {
			writeError(w, err)
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
		page, err := deps.Persist.TemplateTags().List(
			req.Context(),
			persistence.ListPagination{
				Limit:  parseLimit(req, 100),
				Cursor: req.URL.Query().Get("cursor"),
			},
			nil,
		)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]tagItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, tagItem{
				Tag:        r.Tag,
				TemplateID: r.TemplateID,
				UpdatedAt:  r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
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
		existing, err := deps.Persist.TemplateTags().Get(req.Context(), tag, nil)
		if err != nil {
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
		if err := deps.Persist.TemplateTags().Upsert(req.Context(), tag, hash, nil); err != nil {
			writeError(w, err)
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
		deleted, err := deps.Persist.TemplateTags().Delete(req.Context(), tag, nil)
		if err != nil {
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
