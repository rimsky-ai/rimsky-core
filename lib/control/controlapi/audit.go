// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (s *AuthState) EmitKeyCreated(ctx context.Context, p auth.KeyCreatedPayload) {
	s.insertEvent(ctx, auth.EventKeyCreated, p)
}

func (s *AuthState) EmitKeyRevoked(ctx context.Context, p auth.KeyRevokedPayload) {
	s.insertEvent(ctx, auth.EventKeyRevoked, p)
}

func (s *AuthState) EmitKeyRotated(ctx context.Context, p auth.KeyRotatedPayload) {
	s.insertEvent(ctx, auth.EventKeyRotated, p)
}

const auditWriteTimeout = 2 * time.Second

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

func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		if i := strings.Index(h, ","); i > 0 {
			return strings.TrimSpace(h[:i])
		}
		return strings.TrimSpace(h)
	}
	return r.RemoteAddr
}
