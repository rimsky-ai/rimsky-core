// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Auth middleware: the outer IdentityResolver resolves Bearer → identity
// (or anonymous-mode synthetic) and gates pre-action denials; the
// per-handler gateByAction does action-scoped permission checks, sets
// the per-request Mode (execute vs dry_run), and emits audit events.
//
// Why split. Chi's `RouteContext().RoutePattern()` is empty at outer-
// middleware time, so an outer middleware cannot know the action.
// Wrapping each handler at registration time with `gateByAction("<action>",
// handler)` puts the action lookup at a point where it is statically
// known. Defense-in-depth: MCP's in-process Catalog.Invoke re-enters
// the chi router, so the same gate runs again for MCP-originated calls.
//
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

	"github.com/fallguyconsulting/rimsky/foundation/auth"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	foundationshared "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// AuthState is the per-process auth-middleware state. Built once at
// startup; the outer middleware and gateByAction close over it.
type AuthState struct {
	// Tables is the persistence.Tables handle. Used to open
	// transactions for audit appends and to access APIKeys/Events.
	// Required.
	Tables persistence.Tables

	// Registry is the canonical action registry. Required.
	Registry *ActionRegistry

	// Clock is the time source used for active-status predicate
	// evaluation and audit duration measurements. Required.
	Clock foundationshared.Clock

	// Logger is for structured warnings (anonymous-mode banner, audit-
	// insert failures, last_used_at update failures). Required.
	Logger foundationshared.Logger

	// anonCache holds the result of the last anonymous-mode predicate
	// evaluation. TTL bounded at anonCacheTTL.
	anonCache atomic.Pointer[anonCacheEntry]

	// mcpRouterRef is the lazy-bound router pointer the MCP catalog
	// dispatches in-process tool calls through. Populated by
	// registerMCPRoute; NewApp's tail assigns the built router into it.
	mcpRouterRef *routerRef

	// auditDisp is the bounded worker pool that absorbs audit-row
	// inserts off the request hot path. Populated lazily by
	// EnsureAuditDispatcher; tests that prefer synchronous inserts
	// (so they can assert on row presence) can leave it nil and rely
	// on the synchronous fallback in insertEvent.
	auditDisp atomic.Pointer[auditDispatcher]

	// lastUsedUpdates bounds the in-flight UpdateLastUsed goroutine
	// count so a burst of authenticated requests against a slow
	// Postgres can't accumulate thousands of pgx-bound goroutines.
	// Buffered semaphore: gateByAction does a non-blocking acquire;
	// on a full sem the update is skipped with a debug log rather
	// than spawning more goroutines. Initialized lazily under
	// lastUsedOnce so the AuthState zero value remains usable in
	// tests.
	lastUsedOnce    sync.Once
	lastUsedUpdates chan struct{}
}

// lastUsedConcurrencyCap is the maximum in-flight UpdateLastUsed
// goroutines. Sized so steady-state turnover stays within ~one per
// active key; bursts beyond this drop the update with a logged debug
// rather than spawning more goroutines.
const lastUsedConcurrencyCap = 64

type anonCacheEntry struct {
	isAnon bool
	until  time.Time
}

// anonCacheTTL is the staleness bound on the anonymous-mode
// predicate cache. Each control-api replica refreshes independently
// on its own clock; under TTL, post-mint requests may briefly see
// the older state. The handleCreateKey / handleRevokeKey paths call
// InvalidateAnonCache so the same replica's next request sees the
// fresh value immediately.
const anonCacheTTL = 1 * time.Second

// IsAnonymousMode returns whether the deployment currently has zero
// active keys. Uses a short TTL cache to avoid per-request DB hits
// in the unauthenticated fallback path.
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

// InvalidateAnonCache drops the cached predicate. Called by auth
// endpoint handlers after a mutation that could cross the zero
// boundary (create / revoke / rotate / sweep).
func (s *AuthState) InvalidateAnonCache() {
	s.anonCache.Store(nil)
}

// OnAuthMutation drops the cached predicate AND any other per-replica
// soft state that a key-table mutation invalidates. Today this is
// equivalent to InvalidateAnonCache; the named hook is the seam
// SweepRotationGrace calls from in-process tests so the local cache
// reflects the post-sweep state immediately (in production the sweep
// runs in the scheduler process and per-replica caches refresh via
// the anonCacheTTL bound, as the spec accepts).
func (s *AuthState) OnAuthMutation() {
	s.InvalidateAnonCache()
}

// EnsureAuditDispatcher wires the background audit-write worker pool.
// Idempotent; safe to call multiple times. Production wiring calls
// this at startup; tests can opt in or rely on the synchronous-insert
// fallback in insertEvent. The returned *auditDispatcher can be used
// to Stop the workers (drains the queue) — config.StartControlAPI
// hooks this into its shutdown path.
func (s *AuthState) EnsureAuditDispatcher() *auditDispatcher {
	if d := s.auditDisp.Load(); d != nil {
		return d
	}
	d := newAuditDispatcher(s.Tables, s.Logger)
	if !s.auditDisp.CompareAndSwap(nil, d) {
		// Lost the race; another goroutine wired one. Stop ours.
		d.Stop()
		return s.auditDisp.Load()
	}
	return d
}

// dispatcher returns the audit dispatcher (nil if not yet wired).
func (s *AuthState) dispatcher() *auditDispatcher {
	return s.auditDisp.Load()
}

// StopAuditDispatcher closes the dispatcher's queue and waits for
// the workers to drain queued jobs. Safe to call multiple times and
// safe to call when no dispatcher was wired (no-op). Production
// wiring calls this from controlAPIHandle.Shutdown AFTER the HTTP
// server has finished draining so any in-flight handlers have
// already enqueued their final audit rows.
func (s *AuthState) StopAuditDispatcher() {
	d := s.auditDisp.Swap(nil)
	if d == nil {
		return
	}
	d.Stop()
}

// lastUsedSem returns the per-process semaphore for in-flight
// UpdateLastUsed goroutines, lazily initialized on first use.
func (s *AuthState) lastUsedSem() chan struct{} {
	s.lastUsedOnce.Do(func() {
		s.lastUsedUpdates = make(chan struct{}, lastUsedConcurrencyCap)
	})
	return s.lastUsedUpdates
}

// IdentityResolver is the outer chi middleware. It:
//   - extracts Authorization: Bearer <plaintext>
//   - looks up the key by SHA-256(plaintext)
//   - applies the active-status predicate
//   - on success, sets ctxKeyIdentity and falls through
//   - on failure with no anonymous fallback, returns 401 with the
//     appropriate denial_reason and emits auth.access_denied
//
// Does NOT resolve action or check permission — that is gateByAction
// (per-handler), once chi has matched the route.
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
				s.emitDenied(r.Context(), r, start, ident, "", skin, nil, false, http.StatusUnauthorized, denial)
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

// resolveIdentity returns (identity, "", nil) on success, including
// the anonymous-mode synthetic identity. Returns
// (zero-or-row-identity, denialReason, nil) on auth failure. Errors
// are reserved for unexpected DB failures.
func (s *AuthState) resolveIdentity(ctx context.Context, r *http.Request) (auth.Identity, auth.DenialReason, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		// No Authorization header at all → anonymous-mode probe.
		// Distinct from a header with a non-Bearer scheme, which is
		// an explicit (but malformed) credential and must surface
		// as invalid_token so operators can tell the cases apart in
		// the audit log.
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
		// Header present with a scheme rimsky doesn't speak
		// (`Basic`, `Digest`, custom). The client DID send a
		// credential — classify as invalid_token rather than
		// no_token, regardless of anonymous mode (an anonymous
		// deployment that accepts a malformed credential as
		// anonymous would silently mask client bugs).
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

// rowIdentity builds an Identity from a persistence.APIKey row.
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

// gateByAction returns a handler that:
//   - reads the identity placed on ctx by IdentityResolver
//   - checks the identity's grant against the named action
//   - on deny, returns 403 with auth.access_denied audit
//   - on allow, sets ctxKeyMode from the matched entry, runs the
//     inner handler, then emits auth.access_attempted with the
//     captured status code
//   - best-effort updates last_used_at on the key
//
// gateByAction is called at route-registration time with the action
// known statically.
func (s *AuthState) gateByAction(action string, inner http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := s.Clock.Now()
		ident, ok := IdentityFromContextOK(r.Context())
		if !ok {
			// IdentityResolver should have populated this. Surface as 500 — a wiring bug.
			s.Logger.Error("auth.gate.no_identity", "action", action)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "no identity"})
			return
		}
		res := auth.CheckGrant(ident.Permissions, action)
		skin := protocolSkinFromContext(r.Context())
		body, rejected := captureBody(r, w, s.Logger)
		if rejected {
			// 413 already written; do not dispatch the handler.
			return
		}
		// Validate that the captured body is JSON before wrapping it
		// as `request_params`. The audit-row insert path Unmarshals the
		// typed payload back into map[string]any (see audit.go::
		// insertEvent), which fails on malformed JSON and would drop
		// the row. Per spec section "Audit and dry-run", every
		// authenticated request must produce an audit row regardless
		// of body shape — so when the body is non-empty but not JSON
		// (e.g. multipart upload, malformed payload) we land the row
		// with `request_params_invalid: true` and a nil params field.
		var (
			params        json.RawMessage
			paramsInvalid bool
		)
		if len(body) > 0 {
			if json.Valid(body) {
				params = json.RawMessage(body)
			} else {
				paramsInvalid = true
			}
		}
		if !res.Allowed {
			s.emitDenied(r.Context(), r, start, ident, action, skin, params, paramsInvalid, http.StatusForbidden, auth.DenialPermissionDenied)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "permission denied"})
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyMode{}, res.Mode)

		ww := newCapturingWriter(w)
		inner.ServeHTTP(ww, r.WithContext(ctx))

		s.emitAttempted(r.Context(), r, start, ident, action, skin, params, paramsInvalid, ww.status(), res.Mode)

		if ident.KeyID != nil {
			id := *ident.KeyID
			now := start
			// Bounded concurrency: a non-blocking acquire on the
			// semaphore; on a full sem we skip the update with a
			// debug log rather than spawning more goroutines. The
			// last_used_at column is best-effort metadata, not a
			// permission gate, so a missed update is acceptable.
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

// auditBodyCapBytes caps the request body size that the audit
// pipeline records verbatim. JSON bodies above this still flow
// through to the handler unchanged; the audit row records only the
// prefix + a synthetic JSON marker noting the truncation.
// Sized for a few MB — operator-facing JSON requests are well below
// this; the cap exists to prevent a hostile client from buffering
// an arbitrary payload into the audit row.
const auditBodyCapBytes = 4 * 1024 * 1024

// auditBodyHandlerMaxBytes is the absolute ceiling on request-body
// bytes the handler may see. Above this the request is rejected with
// 413 rather than silently truncated. Sized one decimal order above
// the audit cap so legitimate large JSON payloads still flow through
// while a hostile chunked stream can't drive the process out of
// memory.
const auditBodyHandlerMaxBytes = 64 * 1024 * 1024

// captureBody reads the request body up to the handler ceiling and
// re-attaches the FULL captured bytes via NopCloser so the handler can
// re-read them. The audit copy is a separate slice: when the body
// exceeds auditBodyCapBytes the returned audit bytes are a synthetic
// JSON marker recording the truncation; the handler's view of the
// body is never silently corrupted. Bodies above auditBodyHandlerMaxBytes
// trigger a 413 (writing to w directly) — callers must check the
// returned handlerRejected flag and stop dispatch.
//
// Returns:
//   - auditBytes: the bytes to record in the audit row. May be a
//     synthetic JSON marker when truncated; may be nil on empty body.
//   - handlerRejected: true iff the function wrote a 413 response.
//     Callers MUST stop dispatch when true.
func captureBody(r *http.Request, w http.ResponseWriter, logger foundationshared.Logger) (auditBytes []byte, handlerRejected bool) {
	if r.Body == nil || r.ContentLength == 0 {
		return nil, false
	}
	// Limit to handlerCap+1 so we can detect "too large" without
	// buffering an arbitrary client-controlled payload.
	limited := io.LimitReader(r.Body, auditBodyHandlerMaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, false
	}
	if len(body) > auditBodyHandlerMaxBytes {
		// Drain the rest so the underlying connection can be reused,
		// then reject with 413. The handler is never invoked.
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
		return nil, true
	}
	// Always re-attach the full body so the handler reads the same
	// bytes the client sent. Audit truncation is a record-side concern
	// and must not corrupt the handler's view.
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > auditBodyCapBytes {
		if logger != nil {
			logger.Warn("auth.audit_body_truncated",
				"cap_bytes", auditBodyCapBytes,
				"observed_bytes", len(body))
		}
		// Build a synthetic JSON marker so the audit row records the
		// truncation explicitly. Callers consume `requestParams` as
		// json.RawMessage; the marker is well-formed JSON.
		marker := []byte(`{"_audit_truncated":true,"_audit_observed_bytes":` +
			intToString(len(body)) + `}`)
		return marker, false
	}
	return body, false
}

// intToString avoids strconv import bloat in this file; small helper.
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

// capturingWriter wraps http.ResponseWriter so the audit emitter can
// read the response status code after the handler returns.
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
	// http.ResponseWriter writes an implicit 200 if WriteHeader
	// hasn't been called; mirror that for the status capture.
	if !c.wroteHeader {
		c.wroteHeader = true
	}
	return c.ResponseWriter.Write(b)
}

func (c *capturingWriter) status() int { return c.statusCode }

// gate returns an http.HandlerFunc that gates the inner handler on
// the given action when an AuthState is wired. When deps.AuthState
// is nil (tests that pass an `AppDeps{}` literal), the inner handler
// runs unchanged.
//
// Production wiring always installs an AuthState; tests that exercise
// route logic without the auth layer pass a nil AuthState and skip
// the gate.
func gate(deps AppDeps, action string, inner http.HandlerFunc) http.HandlerFunc {
	if deps.AuthState == nil {
		return inner
	}
	return deps.AuthState.gateByAction(action, inner)
}
