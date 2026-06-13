// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Audit-event emit helpers consumed by the auth middleware and the
// auth-endpoint handlers. Writes rows into rimsky_events with the
// `auth.*` kind and the structured payload from foundation/auth.
//
// Audit writes are SYNCHRONOUS and DURABLE: the auth middleware hands
// a fully-built payload to `insertEvent`, which runs the DB
// transaction inline in the request goroutine before the gate
// returns. There is deliberately no queue/worker/buffer here — the
// event log is the canonical forensic record (concept:event-log) and
// must never silently drop a row under load. The per-request latency
// cost is one small INSERT, negligible at control-plane traffic
// volume; see spec:2026-05-29-console-upstream-auth-audit-and-fixes
// section "Event log durability" for the recorded tradeoff.
//
// @concept: event-log

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	eventskinds "github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// emitAttempted writes one auth.access_attempted event.
// paramsInvalid signals the captured request body was non-empty but
// not valid JSON; when set, requestParams must be nil and the audit
// row carries `request_params_invalid: true`.
func (s *AuthState) emitAttempted(
	ctx context.Context,
	r *http.Request,
	start time.Time,
	ident auth.Identity,
	action string,
	skin string,
	requestParams json.RawMessage,
	paramsInvalid bool,
	status int,
	mode auth.Mode,
	isWrite bool,
) {
	elapsed := s.Clock.Now().Sub(start).Milliseconds()
	// `executed` semantics: a read genuinely runs even under dry_run
	// (no mutation to skip), so reads record executed:true whenever they
	// returned cleanly. A write under dry_run skips its mutation, so it
	// records executed:false. isWrite comes from the action registry.
	executed := status < 400 && (!isWrite || mode == auth.ModeExecute)
	p := auth.AccessAttemptedPayload{
		KeyID:                ident.KeyID,
		KeyName:              ident.KeyName,
		IdentityKind:         ident.Kind,
		ProtocolSkin:         skin,
		Action:               action,
		RequestPath:          r.URL.Path,
		RequestMethod:        r.Method,
		RequestParams:        requestParams,
		RequestParamsInvalid: paramsInvalid,
		ResponseStatus:       status,
		Mode:                 mode,
		Executed:             executed,
		DurationMS:           elapsed,
		ClientIP:             clientIP(r),
		UserAgent:            r.Header.Get("User-Agent"),
	}
	s.insertEvent(ctx, auth.EventAccessAttempted, p)
}

// emitDenied writes one auth.access_denied event with the population
// rules from spec section "Population rules for denial rows":
//
//   - For pre-action-resolution denials (no_token, invalid_token,
//     expired_token, revoked_token): Action/RequestParams/Mode null.
//   - For permission_denied: Action and RequestParams populated; Mode
//     null (no matching grant entry, so no mode determined).
//
// paramsInvalid signals the captured request body was non-empty but
// not valid JSON; when set, requestParams must be nil and the audit
// row carries `request_params_invalid: true`.
func (s *AuthState) emitDenied(
	ctx context.Context,
	r *http.Request,
	start time.Time,
	ident auth.Identity,
	action string,
	skin string,
	requestParams json.RawMessage,
	paramsInvalid bool,
	status int,
	reason auth.DenialReason,
) {
	elapsed := s.Clock.Now().Sub(start).Milliseconds()
	p := auth.AccessDeniedPayload{
		ProtocolSkin:         skin,
		RequestPath:          r.URL.Path,
		RequestMethod:        r.Method,
		RequestParams:        requestParams,
		RequestParamsInvalid: paramsInvalid,
		ResponseStatus:       status,
		Executed:             false,
		DurationMS:           elapsed,
		ClientIP:             clientIP(r),
		UserAgent:            r.Header.Get("User-Agent"),
		DenialReason:         reason,
	}
	if ident.KeyID != nil || ident.KeyName != "" {
		kn := ident.KeyName
		kk := ident.Kind
		p.KeyID = ident.KeyID
		p.KeyName = &kn
		p.IdentityKind = &kk
	}
	if action != "" {
		a := action
		p.Action = &a
	}
	s.insertEvent(ctx, auth.EventAccessDenied, p)
}

// EmitKeyCreated writes one auth.key_created event.
func (s *AuthState) EmitKeyCreated(ctx context.Context, p auth.KeyCreatedPayload) {
	s.insertEvent(ctx, auth.EventKeyCreated, p)
}

// EmitKeyRevoked writes one auth.key_revoked event.
func (s *AuthState) EmitKeyRevoked(ctx context.Context, p auth.KeyRevokedPayload) {
	s.insertEvent(ctx, auth.EventKeyRevoked, p)
}

// EmitKeyRotated writes one auth.key_rotated event.
func (s *AuthState) EmitKeyRotated(ctx context.Context, p auth.KeyRotatedPayload) {
	s.insertEvent(ctx, auth.EventKeyRotated, p)
}

// auditWriteTimeout is the per-row DB-write deadline. The synchronous
// insert runs in the request goroutine; the bound caps how long a
// wedged Postgres can hold the request open before the insert errors
// out (which is then surfaced, not swallowed — see insertEvent). It
// mirrors the existing UpdateLastUsed timeout.
const auditWriteTimeout = 2 * time.Second

// insertEvent marshals the typed payload, decodes it back into the
// generic map[string]any shape the EventTable.Append signature wants,
// and writes the row SYNCHRONOUSLY in the calling (request) goroutine.
//
// @blessed-invariant: event-log-canonical-forensic — the event log is the canonical forensic record
// (concept:event-log) — the per-request auth-audit write is durable
// and is never silently dropped. There is no queue/worker/buffer: the
// row lands (or the failure is surfaced) before the gate returns. The
// write is derived from request context (response_status / duration_ms
// are already known), so it runs after the handler returns.
//
// Durability over latency: on insert failure we log at Error (the
// operator-visible signal that the forensic record has a gap) rather
// than dropping silently. The bounded auditWriteTimeout caps the
// per-row cost so a wedged Postgres cannot pin the request goroutine.
func (s *AuthState) insertEvent(_ context.Context, kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.Logger.Error("audit.marshal", "kind", kind, "err", err.Error())
		return
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal(data, &payloadMap); err != nil {
		s.Logger.Error("audit.unmarshal-to-map", "kind", kind, "err", err.Error())
		return
	}
	// The kind string is one of the auth.Event* canonical wire-form
	// constants (e.g. auth.EventAccessAttempted). Parse it back to
	// the typed events.Kind at the persistence boundary so the
	// emit-site discipline (decision:event-log-kind-enum) holds even
	// through this tiny package-internal dispatch wrapper.
	typedKind, err := eventskinds.ParseKindString(kind)
	if err != nil {
		s.Logger.Error("audit.kind-parse", "kind", kind, "err", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	if err := s.Tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return s.Tables.Events().Append(ctx, persistence.EventAppendInput{
			Kind:    typedKind,
			Payload: payloadMap,
		}, tx)
	}); err != nil {
		s.Logger.Error("audit.insert", "kind", kind, "err", err.Error())
	}
}

// clientIP extracts a usable client IP from the request. Prefer the
// first entry in X-Forwarded-For; fall back to RemoteAddr.
func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		if i := strings.Index(h, ","); i > 0 {
			return strings.TrimSpace(h[:i])
		}
		return strings.TrimSpace(h)
	}
	return r.RemoteAddr
}
