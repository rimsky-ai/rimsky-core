// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// APIKey is one row of rimsky_api_keys. See spec
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
// "Persistence schema". The plaintext is never persisted — only its
// SHA-256 hash. Active status is the data-derived predicate
// `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now)
// AND (revoke_at IS NULL OR revoke_at > now)`; the in-grace window
// (revoke_at set but in the future) is still active.
//
// @concept: api-key
type APIKey struct {
	ID             shared.UUID
	KeyHash        []byte // SHA-256(plaintext); 32 bytes
	Name           string
	Permissions    json.RawMessage // grant entries; opaque to persistence
	CreatedAt      time.Time
	CreatedByKeyID *shared.UUID // nil for anonymous-mode mints
	LastUsedAt     *time.Time
	ExpiresAt      *time.Time
	RevokeAt       *time.Time
	RevokedAt      *time.Time
}

// Sentinel errors for APIKeyTable.
var (
	// ErrAPIKeyNameTaken is returned by Insert when the active
	// unique-name partial index conflicts: another active row already
	// holds this name.
	ErrAPIKeyNameTaken = errors.New("persistence: api-key name already taken")

	// ErrAPIKeyHashCollision is returned by Insert when the
	// SHA-256(plaintext) unique index conflicts. SHA-256 over 264
	// bits of plaintext entropy is genuinely impossible to collide
	// at random; in practice this surfaces when a previous deploy
	// left a stale row pointing at the same plaintext (e.g. the
	// caller is re-minting after a partial deploy). Operators
	// recover by nuking and re-migrating the api-keys table — pre-
	// v1, that's the sanctioned remedy per rules.md.
	ErrAPIKeyHashCollision = errors.New("persistence: api-key key_hash collision (likely a stale row from a previous deploy; pre-v1: drop the row and retry)")
)

// APIKeyTable is the rimsky_api_keys accessor. All methods accept a
// nullable Tx; when nil the impl runs against the underlying pool
// (auto-commit). The plan calls out a `WithTx` helper for rotation;
// here we expose it as a separate method on Tables (callers wrap with
// `Tables.Transaction`) so the surface mirrors the rest of the
// persistence package.
//
// Returns of (zero-value, false, nil) mean "no such row". Methods that
// cannot disambiguate distinguish via err vs ok.
//
// @concept: api-key
type APIKeyTable interface {
	// Insert adds a new row. Returns ErrAPIKeyNameTaken when the
	// partial unique-name index collides (another active row holds
	// the same name).
	Insert(ctx context.Context, k APIKey, tx Tx) error

	// GetByID fetches by primary key. Returns (zero, false, nil) on
	// no-such-row.
	GetByID(ctx context.Context, id shared.UUID, tx Tx) (APIKey, bool, error)

	// GetByName fetches the active row for this name — the row in
	// the partial unique-name index (revoked_at IS NULL AND
	// revoke_at IS NULL). Returns (zero, false, nil) when no active
	// row exists.
	GetByName(ctx context.Context, name string, tx Tx) (APIKey, bool, error)

	// GetByHash fetches by SHA-256 hash. Returns (zero, false, nil)
	// on no-match. Does NOT apply the active-status predicate —
	// the auth middleware applies it so it can distinguish
	// expired vs revoked vs in-grace.
	GetByHash(ctx context.Context, hash []byte, tx Tx) (APIKey, bool, error)

	// List enumerates rows ordered by created_at DESC. When
	// includeRevoked=false, rows with revoked_at IS NOT NULL are
	// excluded. nameFilter accepts shell-style glob (`*` and `?`);
	// empty string means no name filter.
	List(ctx context.Context, includeRevoked bool, nameFilter string, tx Tx) ([]APIKey, error)

	// ActiveCount returns the count of rows matching the active-
	// status predicate. Used by the anonymous-mode check and the
	// revoke-the-last-key guard.
	ActiveCount(ctx context.Context, now time.Time, tx Tx) (int, error)

	// MarkRevoked sets revoked_at = now on the given id. Returns:
	//   - (true, true, nil)   the UPDATE mutated the row (newly revoked)
	//   - (false, true, nil)  the row exists but was already revoked
	//                         (idempotent no-op — callers must NOT
	//                         re-emit auth.key_revoked audit events on
	//                         this path; the prior revoker already did)
	//   - (false, false, nil) no such row
	// The (changed, found) split lets handleRevokeKey distinguish the
	// "I revoked it" branch from the "rotation-grace sweep beat me to
	// it" branch and avoid duplicate audit rows.
	MarkRevoked(ctx context.Context, id shared.UUID, now time.Time, tx Tx) (changed bool, found bool, err error)

	// SetRevokeAt sets revoke_at = at on the given id. Used by
	// rotation to push the old row's revoke_at into the grace
	// window so the partial unique-name index releases the name
	// for the new row.
	SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time, tx Tx) error

	// SweepRotationGrace sets revoked_at = now on rows where
	// `revoke_at <= now AND revoked_at IS NULL`. Returns the swept
	// rows (id + name only populated; remaining fields zero) so
	// callers can emit one audit event per swept row.
	SweepRotationGrace(ctx context.Context, now time.Time, tx Tx) ([]APIKey, error)

	// UpdateLastUsed best-effort updates last_used_at on the given
	// id. Errors are not actionable (the auth path proceeds).
	UpdateLastUsed(ctx context.Context, id shared.UUID, now time.Time, tx Tx) error
}
