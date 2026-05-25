// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package auth

import (
	"encoding/json"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// Event kinds for rimsky_events.kind. See spec section "Audit" for
// payload shapes.
const (
	EventAccessAttempted = "auth.access_attempted"
	EventAccessDenied    = "auth.access_denied"
	EventKeyCreated      = "auth.key_created"
	EventKeyRevoked      = "auth.key_revoked"
	EventKeyRotated      = "auth.key_rotated"
)

// DenialReason mirrors the spec's auth.access_denied.denial_reason
// enum exactly.
type DenialReason string

const (
	DenialNoToken          DenialReason = "no_token"
	DenialInvalidToken     DenialReason = "invalid_token"
	DenialExpiredToken     DenialReason = "expired_token"
	DenialRevokedToken     DenialReason = "revoked_token"
	DenialPermissionDenied DenialReason = "permission_denied"
)

// AccessAttemptedPayload is the JSONB body of auth.access_attempted.
type AccessAttemptedPayload struct {
	KeyID         *shared.UUID    `json:"key_id"`
	KeyName       string          `json:"key_name"`
	IdentityKind  IdentityKind    `json:"identity_kind"`
	ProtocolSkin  string          `json:"protocol_skin"` // "http" | "mcp"
	Action        string          `json:"action"`
	RequestPath   string          `json:"request_path"`
	RequestMethod string          `json:"request_method"`
	RequestParams json.RawMessage `json:"request_params,omitempty"`
	// RequestParamsInvalid is true iff the captured request body was
	// non-empty but not valid JSON (e.g. a multipart upload or a
	// malformed body). When set, RequestParams is nil; the audit row
	// still lands so the access attempt is recorded.
	RequestParamsInvalid bool   `json:"request_params_invalid,omitempty"`
	ResponseStatus       int    `json:"response_status"`
	Mode                 Mode   `json:"mode,omitempty"`
	Executed             bool   `json:"executed"`
	DurationMS           int64  `json:"duration_ms"`
	ClientIP             string `json:"client_ip,omitempty"`
	UserAgent            string `json:"user_agent,omitempty"`
}

// AccessDeniedPayload is the JSONB body of auth.access_denied.
// Per spec "Population rules for denial rows" some fields are
// nullable depending on whether denial happened pre- or post-
// action-resolution.
type AccessDeniedPayload struct {
	KeyID         *shared.UUID    `json:"key_id"`
	KeyName       *string         `json:"key_name"`
	IdentityKind  *IdentityKind   `json:"identity_kind"`
	ProtocolSkin  string          `json:"protocol_skin"`
	Action        *string         `json:"action"`
	RequestPath   string          `json:"request_path"`
	RequestMethod string          `json:"request_method"`
	RequestParams json.RawMessage `json:"request_params,omitempty"`
	// RequestParamsInvalid is true iff the captured request body was
	// non-empty but not valid JSON. When set, RequestParams is nil; the
	// audit row still lands so the denied attempt is recorded.
	RequestParamsInvalid bool         `json:"request_params_invalid,omitempty"`
	ResponseStatus       int          `json:"response_status"`
	Mode                 *Mode        `json:"mode"`
	Executed             bool         `json:"executed"`
	DurationMS           int64        `json:"duration_ms"`
	ClientIP             string       `json:"client_ip,omitempty"`
	UserAgent            string       `json:"user_agent,omitempty"`
	DenialReason         DenialReason `json:"denial_reason"`
}

// KeyCreatedPayload is the JSONB body of auth.key_created.
type KeyCreatedPayload struct {
	KeyID          shared.UUID  `json:"key_id"`
	KeyName        string       `json:"key_name"`
	Permissions    Grant        `json:"permissions"`
	CreatedByKeyID *shared.UUID `json:"created_by_key_id"`
	ExpiresAt      *time.Time   `json:"expires_at"`
}

// KeyRevokedReason mirrors the spec's auth.key_revoked.reason enum.
type KeyRevokedReason string

const (
	RevokeReasonManual        KeyRevokedReason = "manual"
	RevokeReasonRotationGrace KeyRevokedReason = "rotation_grace"
	RevokeReasonExpired       KeyRevokedReason = "expired"
)

// KeyRevokedPayload is the JSONB body of auth.key_revoked.
type KeyRevokedPayload struct {
	KeyID          shared.UUID      `json:"key_id"`
	KeyName        string           `json:"key_name"`
	RevokedByKeyID *shared.UUID     `json:"revoked_by_key_id"`
	Reason         KeyRevokedReason `json:"reason"`
}

// KeyRotatedPayload is the JSONB body of auth.key_rotated.
type KeyRotatedPayload struct {
	OldKeyID shared.UUID `json:"old_key_id"`
	NewKeyID shared.UUID `json:"new_key_id"`
	Name     string      `json:"name"`
	RevokeAt time.Time   `json:"revoke_at"`
}
