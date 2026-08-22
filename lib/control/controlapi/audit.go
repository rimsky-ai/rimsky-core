// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	eventskinds "github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func (s *AuthState) emitAttempted(
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
	s.insertEvent(auth.EventAccessAttempted, auth.AccessAttemptedProto(p))
}

func (s *AuthState) emitDenied(
	r *http.Request,
	start time.Time,
	ident auth.Identity,
	action string,
	skin string,
	requestParams json.RawMessage,
	paramsInvalid bool,
	status int,
	reason auth.DenialReason,
	mode *auth.Mode,
) {
	elapsed := s.Clock.Now().Sub(start).Milliseconds()
	p := auth.AccessDeniedPayload{
		ProtocolSkin:         skin,
		RequestPath:          r.URL.Path,
		RequestMethod:        r.Method,
		RequestParams:        requestParams,
		RequestParamsInvalid: paramsInvalid,
		ResponseStatus:       status,
		Mode:                 mode,
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
	s.insertEvent(auth.EventAccessDenied, auth.AccessDeniedProto(p))
}

func (s *AuthState) EmitKeyCreated(ctx context.Context, p auth.KeyCreatedPayload) {
	s.insertEvent(auth.EventKeyCreated, auth.KeyCreatedProto(p))
}

func (s *AuthState) EmitKeyRevoked(ctx context.Context, p auth.KeyRevokedPayload) {
	s.insertEvent(auth.EventKeyRevoked, auth.KeyRevokedProto(p))
}

func (s *AuthState) EmitKeyRotated(ctx context.Context, p auth.KeyRotatedPayload) {
	s.insertEvent(auth.EventKeyRotated, auth.KeyRotatedProto(p))
}

const auditWriteTimeout = 2 * time.Second

// @decision: event-log-kind-enum
func (s *AuthState) insertEvent(kind eventskinds.Kind, payload proto.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	if err := s.Tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return s.Tables.Events().Append(ctx, persistence.EventAppendInput{
			Kind:    kind,
			Payload: eventpayload.New(payload),
		}, tx)
	}); err != nil {
		s.Logger.Error("audit.insert", "kind", kind.String(), "err", err.Error())
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
