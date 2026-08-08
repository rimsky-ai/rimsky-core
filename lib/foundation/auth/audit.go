// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"encoding/json"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
	DenialInvalidDryRun    DenialReason = "invalid_dry_run"
	DenialBodyUnreadable   DenialReason = "body_unreadable"
	DenialBodyTooLarge     DenialReason = "body_too_large"
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
	RevokeAt       time.Time    `json:"revoke_at"`
	RotatedByKeyID *shared.UUID `json:"rotated_by_key_id"`
}

func KeyRevokedProto(p KeyRevokedPayload) *genv1.AuthKeyRevokedPayload {
	return &genv1.AuthKeyRevokedPayload{
		KeyId:          p.KeyID.String(),
		KeyName:        p.KeyName,
		RevokedByKeyId: uuidStringPtr(p.RevokedByKeyID),
		Reason:         string(p.Reason),
	}
}

func KeyCreatedProto(p KeyCreatedPayload) *genv1.AuthKeyCreatedPayload {
	return &genv1.AuthKeyCreatedPayload{
		KeyId:          p.KeyID.String(),
		KeyName:        p.KeyName,
		Permissions:    jsonStruct(p.Permissions),
		CreatedByKeyId: uuidStringPtr(p.CreatedByKeyID),
		ExpiresAt:      timePtrProto(p.ExpiresAt),
	}
}

func KeyRotatedProto(p KeyRotatedPayload) *genv1.AuthKeyRotatedPayload {
	return &genv1.AuthKeyRotatedPayload{
		KeyId:          p.KeyID.String(),
		KeyName:        p.KeyName,
		OldKeyId:       p.OldKeyID.String(),
		NewKeyId:       p.NewKeyID.String(),
		RevokeAt:       timestamppb.New(p.RevokeAt),
		RotatedByKeyId: uuidStringPtr(p.RotatedByKeyID),
	}
}

func AccessAttemptedProto(p AccessAttemptedPayload) *genv1.AuthAccessAttemptedPayload {
	return &genv1.AuthAccessAttemptedPayload{
		KeyId:                uuidStringPtr(p.KeyID),
		KeyName:              p.KeyName,
		IdentityKind:         string(p.IdentityKind),
		ProtocolSkin:         p.ProtocolSkin,
		Action:               p.Action,
		RequestPath:          p.RequestPath,
		RequestMethod:        p.RequestMethod,
		RequestParams:        rawStruct(p.RequestParams),
		RequestParamsInvalid: p.RequestParamsInvalid,
		ResponseStatus:       int32(p.ResponseStatus),
		Mode:                 string(p.Mode),
		Executed:             p.Executed,
		DurationMs:           p.DurationMS,
		ClientIp:             p.ClientIP,
		UserAgent:            p.UserAgent,
	}
}

func AccessDeniedProto(p AccessDeniedPayload) *genv1.AuthAccessDeniedPayload {
	out := &genv1.AuthAccessDeniedPayload{
		KeyId:                uuidStringPtr(p.KeyID),
		KeyName:              p.KeyName,
		ProtocolSkin:         p.ProtocolSkin,
		Action:               p.Action,
		RequestPath:          p.RequestPath,
		RequestMethod:        p.RequestMethod,
		RequestParams:        rawStruct(p.RequestParams),
		RequestParamsInvalid: p.RequestParamsInvalid,
		ResponseStatus:       int32(p.ResponseStatus),
		Executed:             p.Executed,
		DurationMs:           p.DurationMS,
		ClientIp:             p.ClientIP,
		UserAgent:            p.UserAgent,
		DenialReason:         string(p.DenialReason),
	}
	if p.IdentityKind != nil {
		k := string(*p.IdentityKind)
		out.IdentityKind = &k
	}
	if p.Mode != nil {
		m := string(*p.Mode)
		out.Mode = &m
	}
	return out
}

func uuidStringPtr(id *shared.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func timePtrProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func jsonStruct(v any) *structpb.Struct {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return rawStruct(raw)
}

func rawStruct(raw json.RawMessage) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	out := &structpb.Struct{}
	if err := protojson.Unmarshal(raw, out); err != nil {
		return nil
	}
	return out
}
