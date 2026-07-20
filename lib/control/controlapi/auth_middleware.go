// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: api-key
// @concept: permission
// @concept: anonymous-mode

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type AuthState struct {
	Tables persistence.Tables

	Registry *ActionRegistry

	Clock foundationshared.Clock

	Logger foundationshared.Logger

	anonCache atomic.Pointer[anonCacheEntry]

	mcpRouterRef *routerRef

	lastUsedOnce    sync.Once
	lastUsedUpdates chan struct{}
}

const lastUsedConcurrencyCap = 64

type anonCacheEntry struct {
	isAnon bool
	until  time.Time
}

const anonCacheTTL = 1 * time.Second

func (s *AuthState) IsAnonymousMode(ctx context.Context) (bool, error) {
	now := s.Clock.Now()
	if e := s.anonCache.Load(); e != nil && now.Before(e.until) {
		return e.isAnon, nil
	}
	n, err := s.Tables.APIKeys().ActiveCount(ctx, now, nil)
	if err != nil {
		return false, err
	}
	e := &anonCacheEntry{isAnon: n == 0, until: now.Add(anonCacheTTL)}
	s.anonCache.Store(e)
	return e.isAnon, nil
}

func (s *AuthState) InvalidateAnonCache() {
	s.anonCache.Store(nil)
}

func (s *AuthState) OnAuthMutation() {
	s.InvalidateAnonCache()
}

func (s *AuthState) lastUsedSem() chan struct{} {
	s.lastUsedOnce.Do(func() {
		s.lastUsedUpdates = make(chan struct{}, lastUsedConcurrencyCap)
	})
	return s.lastUsedUpdates
}

func (s *AuthState) IdentityResolver() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := s.Clock.Now()
			ident, denial, err := s.resolveIdentity(r.Context(), r)
			if err != nil {
				s.Logger.Error("auth.middleware.error", "err", err.Error())
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth middleware failure"})
				return
			}
			if denial != "" {
				skin := protocolSkinFromContext(r.Context())
				s.emitDenied(r.Context(), r, start, ident, "", skin, nil, false, http.StatusUnauthorized, denial, nil)
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": "unauthorized", "denial_reason": string(denial),
				})
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyIdentity{}, ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *AuthState) resolveIdentity(ctx context.Context, r *http.Request) (auth.Identity, auth.DenialReason, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		anon, err := s.IsAnonymousMode(ctx)
		if err != nil {
			return auth.Identity{}, "", err
		}
		if anon {
			return auth.AnonymousIdentity(), "", nil
		}
		return auth.Identity{}, auth.DenialNoToken, nil
	}
	if !strings.HasPrefix(h, "Bearer ") {
		return auth.Identity{}, auth.DenialInvalidToken, nil
	}
	plaintext := strings.TrimPrefix(h, "Bearer ")
	if err := auth.ValidatePlaintext(plaintext); err != nil {
		return auth.Identity{}, auth.DenialInvalidToken, nil
	}
	h32 := auth.Hash(plaintext)
	row, ok, err := s.Tables.APIKeys().GetByHash(ctx, h32[:], nil)
	if err != nil {
		return auth.Identity{}, "", err
	}
	if !ok {
		return auth.Identity{}, auth.DenialInvalidToken, nil
	}
	now := s.Clock.Now()
	if row.RevokedAt != nil {
		return rowIdentity(row), auth.DenialRevokedToken, nil
	}
	if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
		return rowIdentity(row), auth.DenialExpiredToken, nil
	}
	if row.RevokeAt != nil && !row.RevokeAt.After(now) {
		return rowIdentity(row), auth.DenialRevokedToken, nil
	}
	return rowIdentity(row), "", nil
}

func rowIdentity(row persistence.APIKey) auth.Identity {
	var perms auth.Grant
	if len(row.Permissions) > 0 {
		_ = json.Unmarshal(row.Permissions, &perms)
	}
	id := row.ID
	return auth.Identity{
		KeyID:       &id,
		KeyName:     row.Name,
		Kind:        auth.IdentityAPIKey,
		Permissions: perms,
	}
}

func (s *AuthState) gateByAction(action string, inner http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := s.Clock.Now()
		ident, ok := IdentityFromContextOK(r.Context())
		if !ok {
			s.Logger.Error("auth.gate.no_identity", "action", action)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "no identity"})
			return
		}
		skin := protocolSkinFromContext(r.Context())
		handlerBody, auditBody, rejected := captureBody(r, w, s.Logger)
		if rejected {
			return
		}
		// @concept: permission
		targets := requestTargets(r.Context(), s.Tables, action, handlerBody, r)
		// @concept: permission
		// @concept: dry-run
		var res auth.CheckResult
		for _, target := range targets {
			cand := auth.CheckGrant(ident.Permissions, action, target)
			if !cand.Allowed {
				continue
			}
			if !res.Allowed {
				res = cand
				continue
			}
			if res.Mode == auth.ModeDryRun && cand.Mode == auth.ModeExecute {
				res = cand
			}
		}
		var (
			params        json.RawMessage
			paramsInvalid bool
		)
		if len(auditBody) > 0 {
			if json.Valid(auditBody) {
				params = json.RawMessage(auditBody)
			} else {
				paramsInvalid = true
			}
		}
		requestedMode := auth.ModeExecute
		if r.URL.Query().Get("dry_run") == "true" {
			requestedMode = auth.ModeDryRun
		}
		if !res.Allowed {
			s.emitDenied(r.Context(), r, start, ident, action, skin, params, paramsInvalid, http.StatusForbidden, auth.DenialPermissionDenied, &requestedMode)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "permission denied"})
			return
		}
		mode := requestedMode
		if res.Mode == auth.ModeDryRun {
			mode = auth.ModeDryRun
		}
		ctx := context.WithValue(r.Context(), ctxKeyMode{}, mode)

		entry, known := s.Registry.Entry(action)
		isWrite := !known || entry.IsWrite

		ww := newCapturingWriter(w)
		inner.ServeHTTP(ww, r.WithContext(ctx))

		s.emitAttempted(r.Context(), r, start, ident, action, skin, params, paramsInvalid, ww.status(), mode, isWrite)

		if ident.KeyID != nil {
			id := *ident.KeyID
			now := start
			sem := s.lastUsedSem()
			select {
			case sem <- struct{}{}:
				go func() {
					defer func() { <-sem }()
					bg, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					if err := s.Tables.APIKeys().UpdateLastUsed(bg, id, now, nil); err != nil {
						s.Logger.Debug("auth.last_used_update", "err", err.Error())
					}
				}()
			default:
				s.Logger.Debug("auth.last_used_update_skipped_sem_full", "key_id", id.String())
			}
		}
	}
}

const auditBodyCapBytes = 4 * 1024 * 1024

const auditBodyHandlerMaxBytes = 64 * 1024 * 1024

func captureBody(r *http.Request, w http.ResponseWriter, logger foundationshared.Logger) (handlerBody, auditBytes []byte, handlerRejected bool) {
	if r.Body == nil || r.ContentLength == 0 {
		return nil, nil, false
	}
	limited := io.LimitReader(r.Body, auditBodyHandlerMaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, false
	}
	if len(body) > auditBodyHandlerMaxBytes {
		_, _ = io.Copy(io.Discard, r.Body)
		if logger != nil {
			logger.Warn("auth.audit_body_too_large",
				"handler_cap_bytes", auditBodyHandlerMaxBytes)
		}
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error":             "request body exceeds maximum size",
			"max_bytes":         auditBodyHandlerMaxBytes,
			"observed_at_least": len(body),
		})
		return nil, nil, true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > auditBodyCapBytes {
		if logger != nil {
			logger.Warn("auth.audit_body_truncated",
				"cap_bytes", auditBodyCapBytes,
				"observed_bytes", len(body))
		}
		marker := []byte(`{"_audit_truncated":true,"_audit_observed_bytes":` +
			intToString(len(body)) + `}`)
		return body, marker, false
	}
	return body, body, false
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

type capturingWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func newCapturingWriter(w http.ResponseWriter) *capturingWriter {
	return &capturingWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (c *capturingWriter) WriteHeader(code int) {
	if c.wroteHeader {
		return
	}
	c.statusCode = code
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.wroteHeader = true
	}
	return c.ResponseWriter.Write(b)
}

func (c *capturingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *capturingWriter) status() int { return c.statusCode }

func gate(deps AppDeps, action string, inner http.HandlerFunc) http.HandlerFunc {
	return deps.AuthState.gateByAction(action, inner)
}
