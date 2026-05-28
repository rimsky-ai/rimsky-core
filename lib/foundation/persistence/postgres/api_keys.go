// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.APIKeyTable — Bearer-token API keys for
// control-api auth. See spec
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md.
//
// @concept: api-key

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type apiKeysImpl tablesImpl

var _ persistence.APIKeyTable = (*apiKeysImpl)(nil)

// APIKeys returns the postgres APIKeyTable impl.
func (s *tablesImpl) APIKeys() persistence.APIKeyTable { return (*apiKeysImpl)(s) }

// run returns either the tx-bound querier or the pool, depending on
// whether tx is nil. The API-key surface is reachable from auth-
// middleware contexts that may or may not have an outer transaction
// (rotation runs inside Tables.Transaction; the auth middleware's
// hash-lookup runs against the pool directly).
func (b *apiKeysImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).pool
	}
	return (*tablesImpl)(b).q(tx)
}

const insertAPIKeySQL = `
INSERT INTO rimsky_api_keys (
    id, key_hash, name, permissions, created_at,
    created_by_key_id, expires_at, revoke_at, revoked_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

func (b *apiKeysImpl) Insert(ctx context.Context, k persistence.APIKey, tx persistence.Tx) error {
	perms := k.Permissions
	if len(perms) == 0 {
		perms = []byte("[]")
	}
	_, err := b.run(tx).Exec(ctx, insertAPIKeySQL,
		k.ID, k.KeyHash, k.Name, perms, k.CreatedAt,
		uuidPtrArg(k.CreatedByKeyID),
		timePtrArg(k.ExpiresAt),
		timePtrArg(k.RevokeAt),
		timePtrArg(k.RevokedAt),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "rimsky_api_keys_active_name_idx":
				return persistence.ErrAPIKeyNameTaken
			case "rimsky_api_keys_key_hash_unique":
				// Return the typed sentinel so writeError can map
				// it to a precise client message (and an operator
				// dashboard can distinguish the "stale row from a
				// previous deploy" case from a generic 500).
				return persistence.ErrAPIKeyHashCollision
			}
		}
		return fmt.Errorf("postgres.APIKeys.Insert: %w", err)
	}
	return nil
}

const selectAPIKeyCols = `
	id, key_hash, name, permissions, created_at,
	created_by_key_id, last_used_at, expires_at, revoke_at, revoked_at`

func (b *apiKeysImpl) GetByID(ctx context.Context, id shared.UUID, tx persistence.Tx) (persistence.APIKey, bool, error) {
	row, err := b.run(tx).Query(ctx,
		`SELECT `+selectAPIKeyCols+` FROM rimsky_api_keys WHERE id = $1`, id)
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("postgres.APIKeys.GetByID: %w", err)
	}
	defer row.Close()
	return scanOneAPIKey(row)
}

func (b *apiKeysImpl) GetByName(ctx context.Context, name string, tx persistence.Tx) (persistence.APIKey, bool, error) {
	row, err := b.run(tx).Query(ctx,
		`SELECT `+selectAPIKeyCols+`
		   FROM rimsky_api_keys
		  WHERE name = $1 AND revoked_at IS NULL AND revoke_at IS NULL`, name)
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("postgres.APIKeys.GetByName: %w", err)
	}
	defer row.Close()
	return scanOneAPIKey(row)
}

func (b *apiKeysImpl) GetByHash(ctx context.Context, hash []byte, tx persistence.Tx) (persistence.APIKey, bool, error) {
	row, err := b.run(tx).Query(ctx,
		`SELECT `+selectAPIKeyCols+` FROM rimsky_api_keys WHERE key_hash = $1`, hash)
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("postgres.APIKeys.GetByHash: %w", err)
	}
	defer row.Close()
	return scanOneAPIKey(row)
}

func (b *apiKeysImpl) List(ctx context.Context, includeRevoked bool, nameFilter string, tx persistence.Tx) ([]persistence.APIKey, error) {
	sql := `SELECT ` + selectAPIKeyCols + ` FROM rimsky_api_keys WHERE TRUE`
	args := []any{}
	if !includeRevoked {
		sql += ` AND revoked_at IS NULL`
	}
	if nameFilter != "" {
		args = append(args, globToLike(nameFilter))
		// `ESCAPE '\'` lets globToLike's backslash-escaping protect
		// literal `%` / `_` / `\` in the filter from acting as LIKE
		// wildcards. Without this the operator's filter would match
		// rows it shouldn't.
		sql += fmt.Sprintf(` AND name LIKE $%d ESCAPE '\'`, len(args))
	}
	sql += ` ORDER BY created_at DESC`
	rows, err := b.run(tx).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres.APIKeys.List: %w", err)
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func (b *apiKeysImpl) ActiveCount(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	var n int
	row := b.run(tx).QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_api_keys
		  WHERE revoked_at IS NULL
		    AND (expires_at IS NULL OR expires_at > $1)
		    AND (revoke_at IS NULL OR revoke_at > $1)`, now)
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres.APIKeys.ActiveCount: %w", err)
	}
	return n, nil
}

func (b *apiKeysImpl) MarkRevoked(ctx context.Context, id shared.UUID, now time.Time, tx persistence.Tx) (changed bool, found bool, err error) {
	tag, err := b.run(tx).Exec(ctx,
		`UPDATE rimsky_api_keys
		    SET revoked_at = $2
		  WHERE id = $1 AND revoked_at IS NULL`, id, now)
	if err != nil {
		return false, false, fmt.Errorf("postgres.APIKeys.MarkRevoked: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, true, nil
	}
	// Either already revoked (idempotent no-op) or row missing. Probe
	// existence so the caller can distinguish 404 from "already done"
	// — the latter must NOT re-emit auth.key_revoked.
	var exists bool
	if err := b.run(tx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM rimsky_api_keys WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, false, fmt.Errorf("postgres.APIKeys.MarkRevoked.exists: %w", err)
	}
	return false, exists, nil
}

func (b *apiKeysImpl) SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time, tx persistence.Tx) error {
	_, err := b.run(tx).Exec(ctx,
		`UPDATE rimsky_api_keys SET revoke_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("postgres.APIKeys.SetRevokeAt: %w", err)
	}
	return nil
}

func (b *apiKeysImpl) SweepRotationGrace(ctx context.Context, now time.Time, tx persistence.Tx) ([]persistence.APIKey, error) {
	rows, err := b.run(tx).Query(ctx,
		`UPDATE rimsky_api_keys
		    SET revoked_at = $1
		  WHERE revoke_at IS NOT NULL AND revoke_at <= $1 AND revoked_at IS NULL
		  RETURNING id, name`, now)
	if err != nil {
		return nil, fmt.Errorf("postgres.APIKeys.SweepRotationGrace: %w", err)
	}
	defer rows.Close()
	out := []persistence.APIKey{}
	for rows.Next() {
		var k persistence.APIKey
		if err := rows.Scan(&k.ID, &k.Name); err != nil {
			return nil, fmt.Errorf("postgres.APIKeys.SweepRotationGrace.scan: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *apiKeysImpl) UpdateLastUsed(ctx context.Context, id shared.UUID, now time.Time, tx persistence.Tx) error {
	_, err := b.run(tx).Exec(ctx,
		`UPDATE rimsky_api_keys SET last_used_at = $2 WHERE id = $1`, id, now)
	if err != nil {
		return fmt.Errorf("postgres.APIKeys.UpdateLastUsed: %w", err)
	}
	return nil
}

// scanOneAPIKey reads (at most) one row from rows into APIKey. Returns
// (zero, false, nil) when no rows. Caller must have selected the
// columns in `selectAPIKeyCols` order.
func scanOneAPIKey(rows pgx.Rows) (persistence.APIKey, bool, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return persistence.APIKey{}, false, err
		}
		return persistence.APIKey{}, false, nil
	}
	k, err := scanAPIKeyRow(rows)
	if err != nil {
		return persistence.APIKey{}, false, err
	}
	if rows.Next() {
		return persistence.APIKey{}, false, fmt.Errorf("postgres.APIKeys: unexpected multiple rows")
	}
	if err := rows.Err(); err != nil {
		return persistence.APIKey{}, false, err
	}
	return k, true, nil
}

func scanAPIKeys(rows pgx.Rows) ([]persistence.APIKey, error) {
	out := []persistence.APIKey{}
	for rows.Next() {
		k, err := scanAPIKeyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanAPIKeyRow(rows pgx.Rows) (persistence.APIKey, error) {
	var k persistence.APIKey
	var createdBy *shared.UUID
	var lastUsed, expiresAt, revokeAt, revokedAt *time.Time
	if err := rows.Scan(
		&k.ID, &k.KeyHash, &k.Name, &k.Permissions, &k.CreatedAt,
		&createdBy, &lastUsed, &expiresAt, &revokeAt, &revokedAt,
	); err != nil {
		return persistence.APIKey{}, fmt.Errorf("postgres.APIKeys.scan: %w", err)
	}
	k.CreatedByKeyID = createdBy
	k.LastUsedAt = lastUsed
	k.ExpiresAt = expiresAt
	k.RevokeAt = revokeAt
	k.RevokedAt = revokedAt
	return k, nil
}

func uuidPtrArg(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return *p
}

func timePtrArg(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}

// globToLike translates a shell-style glob (`*`, `?`) into the SQL
// LIKE pattern (`%`, `_`), escaping any embedded LIKE wildcards so a
// literal `%` / `_` / `\` in the source name doesn't accidentally
// match more rows than intended. The server is the canonical filter
// boundary — CLI validation is not load-bearing here (the CLI passes
// the operator's filter through verbatim, so the server has to
// defend itself). Pair with `LIKE ... ESCAPE '\\'` in the query
// builder; callers that bypass globToLike must escape themselves.
func globToLike(glob string) string {
	// Two-phase: first escape the LIKE meta-characters, then map the
	// glob meta-characters to their LIKE equivalents. Doing this in
	// one pass via strings.NewReplacer would map an already-escaped
	// `%` back to `%`. Order matters.
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(glob)
	return strings.NewReplacer("*", "%", "?", "_").Replace(escaped)
}
