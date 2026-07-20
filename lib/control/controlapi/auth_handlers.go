// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		for i := range body.Permissions {
			body.Permissions[i].Action = strings.TrimSpace(body.Permissions[i].Action)
		}
		if err := auth.ValidateGrant(body.Permissions); err != nil {
			badRequest(w, err.Error())
			return
		}
		for _, e := range body.Permissions {
			if e.Action == "*" || strings.HasSuffix(e.Action, ":*") || strings.HasPrefix(e.Action, "*:") {
				continue
			}
			if !deps.AuthState.Registry.IsKnownAction(e.Action) {
				badRequest(w, "unknown action: "+e.Action)
				return
			}
		}
		if err := deps.AuthState.Registry.ValidateGrantScope(body.Permissions); err != nil {
			badRequest(w, err.Error())
			return
		}
		activeCount, err := deps.AuthState.Tables.APIKeys().ActiveCount(r.Context(), deps.AuthState.Clock.Now(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		wouldExitAnonymous := activeCount == 0
		forceExpiring := r.URL.Query().Get("force_expiring_key") == "true"
		if wouldExitAnonymous && body.ExpiresAt != nil && !forceExpiring {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "creating the deployment's first API key with an expiry risks a silent return to anonymous mode when it lapses; pass ?force_expiring_key=true to confirm, or omit expires_at for a permanent key",
			})
			return
		}
		if ModeFromContext(r.Context()) == auth.ModeDryRun {
			details := map[string]any{
				"key_id":      "dry-run-not-persisted",
				"name":        body.Name,
				"permissions": body.Permissions,
			}
			if wouldExitAnonymous {
				details["note"] = "this is the first key; committing it exits anonymous mode and requires auth on all future requests"
			}
			WriteDryRunResponseForced(w, "would_have_created_key", details)
			return
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
		if ModeFromContext(ctx) == auth.ModeDryRun {
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
			WriteDryRunResponseForced(w, "would_have_revoked_key", map[string]any{
				"key_id": row.ID.String(),
			})
			return
		}
		outcome, err := keys.RevokeIfNotLast(ctx, row.ID, now, force, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		switch outcome {
		case persistence.RevokeResultNotFound:
			notFoundResp(w, "no such key")
			return
		case persistence.RevokeResultWouldLeaveNoneActive:
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":             "would leave zero active keys (anonymous mode); pass ?force_leave_anonymous=true to confirm",
				"active_keys_after": 0,
			})
			return
		case persistence.RevokeResultAlreadyRevoked:
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
		now := deps.AuthState.Clock.Now()
		if oldRow.ExpiresAt != nil && r.URL.Query().Get("force_expiring_key") != "true" {
			active, err := keys.ActiveCount(ctx, now, nil)
			if err != nil {
				writeError(w, err)
				return
			}
			thisRowActive := oldRow.RevokedAt == nil &&
				(oldRow.ExpiresAt == nil || oldRow.ExpiresAt.After(now)) &&
				(oldRow.RevokeAt == nil || oldRow.RevokeAt.After(now))
			if thisRowActive && active <= 1 {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": "rotating the deployment's only active API key would inherit its expiry and risks a silent return to anonymous mode when it lapses; pass ?force_expiring_key=true to confirm",
				})
				return
			}
		}
		if WriteDryRunResponse(w, r, "would_have_rotated_key", map[string]any{
			"key_id": oldRow.ID.String(),
		}) {
			return
		}
		plaintext, hash, err := auth.Mint()
		if err != nil {
			writeError(w, err)
			return
		}
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
			KeyID:    newRow.ID,
			KeyName:  oldRow.Name,
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

func handleWhoAmI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := IdentityFromContextOK(r.Context())
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "no identity"})
			return
		}
		resp := map[string]any{
			"kind":     string(ident.Kind),
			"key_name": ident.KeyName,
		}
		if ident.KeyID != nil {
			resp["key_id"] = ident.KeyID.String()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func lookupByNameOrID(ctx context.Context, t persistence.APIKeyTable, nameOrID string) (persistence.APIKey, bool, error) {
	if id, err := uuid.Parse(nameOrID); err == nil {
		return t.GetByID(ctx, id, nil)
	}
	return t.GetByName(ctx, nameOrID, nil)
}
