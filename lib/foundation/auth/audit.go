// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var (
	EventAccessAttempted = events.KindAuthAccessAttempted().String()
	EventAccessDenied    = events.KindAuthAccessDenied().String()
	EventKeyCreated      = events.KindAuthKeyCreated().String()
	EventKeyRevoked      = events.KindAuthKeyRevoked().String()
	EventKeyRotated      = events.KindAuthKeyRotated().String()
)

type DenialReason string

const (
	DenialNoToken          DenialReason = "no_token"
	DenialInvalidToken     DenialReason = "invalid_token"
	DenialExpiredToken     DenialReason = "expired_token"
	DenialRevokedToken     DenialReason = "revoked_token"
	DenialPermissionDenied DenialReason = "permission_denied"
)

type AccessAttemptedPayload struct {
	KeyID                *shared.UUID    `json:"key_id"`
	KeyName              string          `json:"key_name"`
	IdentityKind         IdentityKind    `json:"identity_kind"`
	ProtocolSkin         string          `json:"protocol_skin"`
	Action               string          `json:"action"`
	RequestPath          string          `json:"request_path"`
	RequestMethod        string          `json:"request_method"`
	RequestParams        json.RawMessage `json:"request_params,omitempty"`
	RequestParamsInvalid bool            `json:"request_params_invalid,omitempty"`
	ResponseStatus       int             `json:"response_status"`
	Mode                 Mode            `json:"mode,omitempty"`
	Executed             bool            `json:"executed"`
	DurationMS           int64           `json:"duration_ms"`
	ClientIP             string          `json:"client_ip,omitempty"`
	UserAgent            string          `json:"user_agent,omitempty"`
}

type AccessDeniedPayload struct {
	KeyID                *shared.UUID    `json:"key_id"`
	KeyName              *string         `json:"key_name"`
	IdentityKind         *IdentityKind   `json:"identity_kind"`
	ProtocolSkin         string          `json:"protocol_skin"`
	Action               *string         `json:"action"`
	RequestPath          string          `json:"request_path"`
	RequestMethod        string          `json:"request_method"`
	RequestParams        json.RawMessage `json:"request_params,omitempty"`
	RequestParamsInvalid bool            `json:"request_params_invalid,omitempty"`
	ResponseStatus       int             `json:"response_status"`
	Mode                 *Mode           `json:"mode"`
	Executed             bool            `json:"executed"`
	DurationMS           int64           `json:"duration_ms"`
	ClientIP             string          `json:"client_ip,omitempty"`
	UserAgent            string          `json:"user_agent,omitempty"`
	DenialReason         DenialReason    `json:"denial_reason"`
}

type KeyCreatedPayload struct {
	KeyID          shared.UUID  `json:"key_id"`
	KeyName        string       `json:"key_name"`
	Permissions    Grant        `json:"permissions"`
	CreatedByKeyID *shared.UUID `json:"created_by_key_id"`
	ExpiresAt      *time.Time   `json:"expires_at"`
}

type KeyRevokedReason string

const (
	RevokeReasonManual        KeyRevokedReason = "manual"
	RevokeReasonRotationGrace KeyRevokedReason = "rotation_grace"
)

type KeyRevokedPayload struct {
	KeyID          shared.UUID      `json:"key_id"`
	KeyName        string           `json:"key_name"`
	RevokedByKeyID *shared.UUID     `json:"revoked_by_key_id"`
	Reason         KeyRevokedReason `json:"reason"`
}

type KeyRotatedPayload struct {
	KeyID          shared.UUID  `json:"key_id"`
	KeyName        string       `json:"key_name"`
	OldKeyID       shared.UUID  `json:"old_key_id"`
	NewKeyID       shared.UUID  `json:"new_key_id"`
	Name           string       `json:"name"`
	RevokeAt       time.Time    `json:"revoke_at"`
	RotatedByKeyID *shared.UUID `json:"rotated_by_key_id"`
}
