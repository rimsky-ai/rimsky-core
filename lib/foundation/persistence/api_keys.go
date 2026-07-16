// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: api-key
type APIKey struct {
	ID             shared.UUID
	KeyHash        []byte
	Name           string
	Permissions    json.RawMessage
	CreatedAt      time.Time
	CreatedByKeyID *shared.UUID
	LastUsedAt     *time.Time
	ExpiresAt      *time.Time
	RevokeAt       *time.Time
	RevokedAt      *time.Time
}

var (
	ErrAPIKeyNameTaken = errors.New("persistence: api-key name already taken")

	ErrAPIKeyHashCollision = errors.New("persistence: api-key key_hash collision (likely a stale row from a previous deploy; pre-v1: drop the row and retry)")
)

type RevokeResult int

const (
	RevokeResultNotFound RevokeResult = iota
	RevokeResultAlreadyRevoked
	RevokeResultWouldLeaveNoneActive
	RevokeResultRevoked
)

// @concept: api-key
type APIKeyTable interface {
	Insert(ctx context.Context, k APIKey, tx Tx) error

	GetByID(ctx context.Context, id shared.UUID, tx Tx) (APIKey, bool, error)

	GetByName(ctx context.Context, name string, tx Tx) (APIKey, bool, error)

	GetByHash(ctx context.Context, hash []byte, tx Tx) (APIKey, bool, error)

	List(ctx context.Context, includeRevoked bool, nameFilter string, tx Tx) ([]APIKey, error)

	ActiveCount(ctx context.Context, now time.Time, tx Tx) (int, error)

	MarkRevoked(ctx context.Context, id shared.UUID, now time.Time, tx Tx) (changed bool, found bool, err error)

	RevokeIfNotLast(ctx context.Context, id shared.UUID, now time.Time, force bool, tx Tx) (RevokeResult, error)

	SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time, tx Tx) error

	SweepRotationGrace(ctx context.Context, now time.Time, tx Tx) ([]APIKey, error)

	UpdateLastUsed(ctx context.Context, id shared.UUID, now time.Time, tx Tx) error
}
