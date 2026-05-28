// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// HTTP handlers backing the /auth/keys/* surface. See spec
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
// "Control-api endpoints / Auth endpoints".
//
// @concept: api-key

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// keyDTO is the public JSON shape for an API key (no plaintext).
type keyDTO struct {
	ID             shared.UUID  `json:"id"`
	Name           string       `json:"name"`
	Permissions    auth.Grant   `json:"permissions"`
	CreatedAt      time.Time    `json:"created_at"`
	CreatedByKeyID *shared.UUID `json:"created_by_key_id,omitempty"`
	LastUsedAt     *time.Time   `json:"last_used_at,omitempty"`
	ExpiresAt      *time.Time   `json:"expires_at,omitempty"`
	RevokeAt       *time.Time   `json:"revoke_at,omitempty"`
	RevokedAt      *time.Time   `json:"revoked_at,omitempty"`
}

func rowToDTO(row persistence.APIKey) keyDTO {
	var perms auth.Grant
	if len(row.Permissions) > 0 {
		_ = json.Unmarshal(row.Permissions, &perms)
	}
	return keyDTO{
		ID:             row.ID,
		Name:           row.Name,
		Permissions:    perms,
		CreatedAt:      row.CreatedAt,
		CreatedByKeyID: row.CreatedByKeyID,
		LastUsedAt:     row.LastUsedAt,
		ExpiresAt:      row.ExpiresAt,
		RevokeAt:       row.RevokeAt,
		RevokedAt:      row.RevokedAt,
	}
}

// handleCreateKey backs POST /auth/keys. Mints a new key; surfaces
// the plaintext exactly once. Validates the requested grant against
// the action registry (wildcards are accepted without registry
// lookup; exact action strings must be registered).
//
// Note (spec K12): auth mutations are NOT dry-runnable in V1. The
// handler intentionally ignores ModeFromContext — a `mode: dry_run`
// grant entry for `auth:create` is parsed and stored but the handler
// always executes. Rationale: a dry-run create wouldn't change the
// active-key count, leading to confusing audit trails interacting
// with the implicit-anonymous predicate.
func handleCreateKey(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type req struct {
			Name        string     `json:"name"`
			Permissions auth.Grant `json:"permissions"`
			ExpiresAt   *time.Time `json:"expires_at,omitempty"`
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			badRequest(w, "name is required")
			return
		}
		// Canonicalize: trim whitespace on action strings before
		// validation so an operator's accidental `"node:invalidate "`
		// surfaces as either accepted (after trim) or rejected with
		// a precise error rather than a confusing
		// `unknown action: node:invalidate ` (trailing space).
		for i := range body.Permissions {
			body.Permissions[i].Action = strings.TrimSpace(body.Permissions[i].Action)
		}
		if err := auth.ValidateGrant(body.Permissions); err != nil {
			badRequest(w, err.Error())
			return
		}
		// Reject any exact action string that isn't in the registry.
		// Wildcards (`*`, `<noun>:*`, `*:<verb>`) are accepted without
		// registry lookup — they match what's registered at request
		// time, not at mint time.
		for _, e := range body.Permissions {
			if e.Action == "*" || strings.HasSuffix(e.Action, ":*") || strings.HasPrefix(e.Action, "*:") {
				continue
			}
			if !deps.AuthState.Registry.IsKnownAction(e.Action) {
				badRequest(w, "unknown action: "+e.Action)
				return
			}
		}
		plaintext, hash, err := auth.Mint()
		if err != nil {
			writeError(w, err)
			return
		}
		permsJSON, _ := json.Marshal(body.Permissions)
		ident, _ := IdentityFromContextOK(r.Context())
		var createdBy *shared.UUID
		if ident.KeyID != nil {
			createdBy = ident.KeyID
		}
		row := persistence.APIKey{
			ID:             uuid.New(),
			KeyHash:        hash[:],
			Name:           body.Name,
			Permissions:    permsJSON,
			CreatedAt:      deps.AuthState.Clock.Now(),
			CreatedByKeyID: createdBy,
			ExpiresAt:      body.ExpiresAt,
		}
		err = deps.AuthState.Tables.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.AuthState.Tables.APIKeys().Insert(ctx, row, tx)
		})
		if err != nil {
			if errors.Is(err, persistence.ErrAPIKeyNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "name already in use"})
				return
			}
			if errors.Is(err, persistence.ErrAPIKeyHashCollision) {
				// Genuinely impossible at random for SHA-256 over
				// 264 random bits; in practice this surfaces when a
				// previous deploy left a stale row. 500 is the
				// honest status code (the operator's
				// configuration's drifted, not the request's), but
				// the error body carries the recovery path so
				// operator dashboards aren't left guessing.
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":    "api-key key_hash collision",
					"hint":     "most likely a stale row from a previous deploy; pre-v1 remedy is to drop the conflicting row and retry the mint",
					"sentinel": "ErrAPIKeyHashCollision",
				})
				return
			}
			writeError(w, err)
			return
		}
		deps.AuthState.InvalidateAnonCache()
		deps.AuthState.EmitKeyCreated(r.Context(), auth.KeyCreatedPayload{
			KeyID:          row.ID,
			KeyName:        row.Name,
			Permissions:    body.Permissions,
			CreatedByKeyID: createdBy,
			ExpiresAt:      row.ExpiresAt,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          row.ID,
			"name":        row.Name,
			"plaintext":   plaintext,
			"permissions": body.Permissions,
			"created_at":  row.CreatedAt,
			"expires_at":  row.ExpiresAt,
		})
	}
}

// handleListKeys backs GET /auth/keys.
func handleListKeys(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nameFilter := r.URL.Query().Get("name_filter")
		includeRevoked := r.URL.Query().Get("include_revoked") == "true"
		rows, err := deps.AuthState.Tables.APIKeys().List(r.Context(), includeRevoked, nameFilter, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]keyDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, rowToDTO(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": out})
	}
}

// handleShowKey backs GET /auth/keys/{nameOrID}.
func handleShowKey(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nameOrID := chi.URLParam(r, "nameOrID")
		row, ok, err := lookupByNameOrID(r.Context(), deps.AuthState.Tables.APIKeys(), nameOrID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !ok {
			notFoundResp(w, "no such key")
			return
		}
		writeJSON(w, http.StatusOK, rowToDTO(row))
	}
}

// handleRevokeKey backs DELETE /auth/keys/{nameOrID}. Refuses if the
// revocation would leave zero active keys unless
// ?force_leave_anonymous=true is set.
func handleRevokeKey(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		nameOrID := chi.URLParam(r, "nameOrID")
		force := r.URL.Query().Get("force_leave_anonymous") == "true"
		keys := deps.AuthState.Tables.APIKeys()
		row, ok, err := lookupByNameOrID(ctx, keys, nameOrID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !ok {
			notFoundResp(w, "no such key")
			return
		}
		now := deps.AuthState.Clock.Now()
		active, err := keys.ActiveCount(ctx, now, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		thisRowActive := row.RevokedAt == nil &&
			(row.ExpiresAt == nil || row.ExpiresAt.After(now)) &&
			(row.RevokeAt == nil || row.RevokeAt.After(now))
		if thisRowActive && active <= 1 && !force {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":             "would leave zero active keys (anonymous mode); pass ?force_leave_anonymous=true to confirm",
				"active_keys_after": 0,
			})
			return
		}
		changed, found, err := keys.MarkRevoked(ctx, row.ID, now, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if !found {
			// Row was deleted between the lookup above and the
			// UPDATE — vanishingly unlikely pre-v1 (no DELETE path
			// exists), but keep the branch defensive.
			notFoundResp(w, "no such key")
			return
		}
		if !changed {
			// Row exists but a prior revoker (typically the
			// rotation-grace sweep) already set revoked_at. The
			// original revoker already fired auth.key_revoked +
			// dropped the anon cache; emitting another row here
			// would double-count this revocation in the audit log
			// with a misleading reason="manual". Return 200 with
			// `already_revoked: true` so operators can tell which
			// path landed.
			writeJSON(w, http.StatusOK, map[string]any{
				"id":              row.ID,
				"name":            row.Name,
				"already_revoked": true,
			})
			return
		}
		deps.AuthState.InvalidateAnonCache()
		ident, _ := IdentityFromContextOK(ctx)
		var revokedBy *shared.UUID
		if ident.KeyID != nil {
			revokedBy = ident.KeyID
		}
		deps.AuthState.EmitKeyRevoked(ctx, auth.KeyRevokedPayload{
			KeyID:          row.ID,
			KeyName:        row.Name,
			RevokedByKeyID: revokedBy,
			Reason:         auth.RevokeReasonManual,
		})
		writeJSON(w, http.StatusOK, map[string]any{"id": row.ID, "name": row.Name})
	}
}

// handleRotateKey backs POST /auth/keys/{nameOrID}/rotate. Atomic:
// inside one tx, sets revoke_at on the old row (so it drops out of
// the partial unique-name index) and inserts the new row with the
// same name + permissions.
func handleRotateKey(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		nameOrID := chi.URLParam(r, "nameOrID")
		type req struct {
			Grace string `json:"grace,omitempty"`
		}
		var body req
		_ = json.NewDecoder(r.Body).Decode(&body)
		grace := 24 * time.Hour
		if body.Grace != "" {
			d, err := time.ParseDuration(body.Grace)
			if err != nil {
				badRequest(w, "invalid grace duration: "+err.Error())
				return
			}
			grace = d
		}
		keys := deps.AuthState.Tables.APIKeys()
		oldRow, ok, err := lookupByNameOrID(ctx, keys, nameOrID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !ok {
			notFoundResp(w, "no such key")
			return
		}
		if oldRow.RevokedAt != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "cannot rotate a revoked key"})
			return
		}
		plaintext, hash, err := auth.Mint()
		if err != nil {
			writeError(w, err)
			return
		}
		now := deps.AuthState.Clock.Now()
		revokeAt := now.Add(grace)
		newRow := persistence.APIKey{
			ID:          uuid.New(),
			KeyHash:     hash[:],
			Name:        oldRow.Name,
			Permissions: oldRow.Permissions,
			CreatedAt:   now,
			ExpiresAt:   oldRow.ExpiresAt,
		}
		err = deps.AuthState.Tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := keys.SetRevokeAt(ctx, oldRow.ID, revokeAt, tx); err != nil {
				return err
			}
			return keys.Insert(ctx, newRow, tx)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		deps.AuthState.InvalidateAnonCache()
		deps.AuthState.EmitKeyRotated(ctx, auth.KeyRotatedPayload{
			OldKeyID: oldRow.ID,
			NewKeyID: newRow.ID,
			Name:     oldRow.Name,
			RevokeAt: revokeAt,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"old_key_id": oldRow.ID,
			"new_key_id": newRow.ID,
			"name":       newRow.Name,
			"plaintext":  plaintext,
			"revoke_at":  revokeAt,
		})
	}
}

// handleAuthStatus backs GET /auth/status. Returns the deployment's
// auth mode + key counts.
func handleAuthStatus(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		keys := deps.AuthState.Tables.APIKeys()
		now := deps.AuthState.Clock.Now()
		active, err := keys.ActiveCount(ctx, now, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		// Sum admin keys by listing all active keys and inspecting
		// their grants. Cheap in V1 (few keys); cache by V2 if it
		// becomes a hot path.
		//
		// Definition of admin: the grant contains the literal `*`
		// entry. Expansions that cover the same surface (e.g.
		// `[*:read, *:write, ...]`) do NOT count — operators relying
		// on those expansions should mint a true `*` grant if they
		// want it reflected here. The spec section "auth/status"
		// reserves "admin_count" for the literal-`*` definition;
		// keeping the loop narrow keeps the metric stable across
		// future wildcard-expansion changes.
		rows, err := keys.List(ctx, false, "", nil)
		if err != nil {
			writeError(w, err)
			return
		}
		admins := 0
		for _, row := range rows {
			if row.RevokedAt != nil {
				continue
			}
			if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
				continue
			}
			if row.RevokeAt != nil && !row.RevokeAt.After(now) {
				continue
			}
			var grant auth.Grant
			if err := json.Unmarshal(row.Permissions, &grant); err != nil {
				continue
			}
			for _, e := range grant {
				if e.Action == "*" {
					admins++
					break
				}
			}
		}
		mode := "authenticated"
		if active == 0 {
			mode = "anonymous"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":             mode,
			"active_key_count": active,
			"admin_count":      admins,
		})
	}
}

// lookupByNameOrID resolves a path segment that may be a UUID or a
// human-readable name. Tries UUID parse first; falls back to GetByName.
func lookupByNameOrID(ctx context.Context, t persistence.APIKeyTable, nameOrID string) (persistence.APIKey, bool, error) {
	if id, err := uuid.Parse(nameOrID); err == nil {
		return t.GetByID(ctx, id, nil)
	}
	return t.GetByName(ctx, nameOrID, nil)
}
