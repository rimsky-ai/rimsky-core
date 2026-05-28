// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.APIKeyTable — Bearer-token API keys for
// control-api auth. Mirror of the postgres impl. See spec
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md.
//
// @concept: api-key

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	sqlite3 "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// sqliteConstraintUnique mirrors `SQLITE_CONSTRAINT_UNIQUE` from
// modernc.org/sqlite/lib. Pinned here to avoid an internal-package
// import (the lib subpackage isn't blessed for direct use); the
// value is part of SQLite's public extended-result-code surface and
// has been stable since the 3.x series.
const sqliteConstraintUnique = 2067

type apiKeysImpl tablesImpl

var _ persistence.APIKeyTable = (*apiKeysImpl)(nil)

// APIKeys returns the sqlite APIKeyTable impl.
func (s *tablesImpl) APIKeys() persistence.APIKeyTable { return (*apiKeysImpl)(s) }

// run returns the tx-bound querier when tx is non-nil, else the
// underlying *sql.DB. The API-key surface is reachable from auth-
// middleware contexts that may or may not have an outer transaction
// (rotation runs inside Tables.Transaction; the auth middleware's
// hash-lookup runs against the DB directly).
func (b *apiKeysImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).db
	}
	return (*tablesImpl)(b).q(tx)
}

const sqliteInsertAPIKeySQL = `
INSERT INTO rimsky_api_keys (
    id, key_hash, name, permissions, created_at,
    created_by_key_id, expires_at, revoke_at, revoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (b *apiKeysImpl) Insert(ctx context.Context, k persistence.APIKey, tx persistence.Tx) error {
	perms := k.Permissions
	if len(perms) == 0 {
		perms = []byte("[]")
	}
	createdAt := k.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := b.run(tx).ExecContext(ctx, sqliteInsertAPIKeySQL,
		k.ID.String(), k.KeyHash, k.Name, string(perms), createdAt,
		uuidPtrStr(k.CreatedByKeyID),
		timePtrStr(k.ExpiresAt),
		timePtrStr(k.RevokeAt),
		timePtrStr(k.RevokedAt),
	)
	if err != nil {
		// Prefer the structured error code over a substring match
		// on the error message: modernc.org/sqlite's wording for
		// UNIQUE constraint violations has shifted between releases,
		// and a future bump could silently misroute the partial
		// unique-name index error as an "impossible key_hash
		// collision" 500.
		var sErr *sqlite3.Error
		isUnique := errors.As(err, &sErr) && sErr.Code() == sqliteConstraintUnique
		// Fall back to a message match for older driver versions
		// that don't expose *sqlite3.Error on this code path; the
		// substring shape is stable enough for the secondary path.
		if !isUnique {
			isUnique = strings.Contains(err.Error(), "UNIQUE")
		}
		if isUnique {
			// The error message still carries the column / index
			// name (modernc.org/sqlite passes the SQLite-emitted
			// "UNIQUE constraint failed: rimsky_api_keys.<col>"
			// text through verbatim). Distinguish hash-collision
			// from the partial unique-name index by column.
			if strings.Contains(err.Error(), "rimsky_api_keys.key_hash") {
				return persistence.ErrAPIKeyHashCollision
			}
			return persistence.ErrAPIKeyNameTaken
		}
		return fmt.Errorf("sqlite.APIKeys.Insert: %w", err)
	}
	return nil
}

const sqliteSelectAPIKeyCols = `
	id, key_hash, name, permissions, created_at,
	created_by_key_id, last_used_at, expires_at, revoke_at, revoked_at`

func (b *apiKeysImpl) GetByID(ctx context.Context, id shared.UUID, tx persistence.Tx) (persistence.APIKey, bool, error) {
	rows, err := b.run(tx).QueryContext(ctx,
		`SELECT `+sqliteSelectAPIKeyCols+` FROM rimsky_api_keys WHERE id = ?`, id.String())
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("sqlite.APIKeys.GetByID: %w", err)
	}
	defer rows.Close()
	return scanOneAPIKey(rows)
}

func (b *apiKeysImpl) GetByName(ctx context.Context, name string, tx persistence.Tx) (persistence.APIKey, bool, error) {
	rows, err := b.run(tx).QueryContext(ctx,
		`SELECT `+sqliteSelectAPIKeyCols+`
		   FROM rimsky_api_keys
		  WHERE name = ? AND revoked_at IS NULL AND revoke_at IS NULL`, name)
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("sqlite.APIKeys.GetByName: %w", err)
	}
	defer rows.Close()
	return scanOneAPIKey(rows)
}

func (b *apiKeysImpl) GetByHash(ctx context.Context, hash []byte, tx persistence.Tx) (persistence.APIKey, bool, error) {
	rows, err := b.run(tx).QueryContext(ctx,
		`SELECT `+sqliteSelectAPIKeyCols+` FROM rimsky_api_keys WHERE key_hash = ?`, hash)
	if err != nil {
		return persistence.APIKey{}, false, fmt.Errorf("sqlite.APIKeys.GetByHash: %w", err)
	}
	defer rows.Close()
	return scanOneAPIKey(rows)
}

func (b *apiKeysImpl) List(ctx context.Context, includeRevoked bool, nameFilter string, tx persistence.Tx) ([]persistence.APIKey, error) {
	sqlStr := `SELECT ` + sqliteSelectAPIKeyCols + ` FROM rimsky_api_keys WHERE 1=1`
	args := []any{}
	if !includeRevoked {
		sqlStr += ` AND revoked_at IS NULL`
	}
	if nameFilter != "" {
		args = append(args, globToLike(nameFilter))
		// ESCAPE '\\' matches the backslash-escaping globToLike does
		// on literal LIKE meta-characters in the input.
		sqlStr += ` AND name LIKE ? ESCAPE '\'`
	}
	sqlStr += ` ORDER BY created_at DESC`
	rows, err := b.run(tx).QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite.APIKeys.List: %w", err)
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func (b *apiKeysImpl) ActiveCount(ctx context.Context, now time.Time, tx persistence.Tx) (int, error) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	var n int
	row := b.run(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_api_keys
		  WHERE revoked_at IS NULL
		    AND (expires_at IS NULL OR expires_at > ?)
		    AND (revoke_at IS NULL OR revoke_at > ?)`, nowStr, nowStr)
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite.APIKeys.ActiveCount: %w", err)
	}
	return n, nil
}

func (b *apiKeysImpl) MarkRevoked(ctx context.Context, id shared.UUID, now time.Time, tx persistence.Tx) (changed bool, found bool, err error) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	res, err := b.run(tx).ExecContext(ctx,
		`UPDATE rimsky_api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		nowStr, id.String())
	if err != nil {
		return false, false, fmt.Errorf("sqlite.APIKeys.MarkRevoked: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return true, true, nil
	}
	// Either already revoked (idempotent no-op) or row missing. The
	// (changed, found) split lets handleRevokeKey skip a duplicate
	// auth.key_revoked emit on the already-revoked path.
	var exists int
	if err := b.run(tx).QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM rimsky_api_keys WHERE id = ?)`, id.String()).Scan(&exists); err != nil {
		return false, false, fmt.Errorf("sqlite.APIKeys.MarkRevoked.exists: %w", err)
	}
	return false, exists != 0, nil
}

func (b *apiKeysImpl) SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time, tx persistence.Tx) error {
	_, err := b.run(tx).ExecContext(ctx,
		`UPDATE rimsky_api_keys SET revoke_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), id.String())
	if err != nil {
		return fmt.Errorf("sqlite.APIKeys.SetRevokeAt: %w", err)
	}
	return nil
}

func (b *apiKeysImpl) SweepRotationGrace(ctx context.Context, now time.Time, tx persistence.Tx) ([]persistence.APIKey, error) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	// sqlite supports RETURNING since 3.35.
	rows, err := b.run(tx).QueryContext(ctx,
		`UPDATE rimsky_api_keys
		    SET revoked_at = ?
		  WHERE revoke_at IS NOT NULL AND revoke_at <= ? AND revoked_at IS NULL
		  RETURNING id, name`, nowStr, nowStr)
	if err != nil {
		return nil, fmt.Errorf("sqlite.APIKeys.SweepRotationGrace: %w", err)
	}
	defer rows.Close()
	out := []persistence.APIKey{}
	for rows.Next() {
		var idStr, name string
		if err := rows.Scan(&idStr, &name); err != nil {
			return nil, fmt.Errorf("sqlite.APIKeys.SweepRotationGrace.scan: %w", err)
		}
		u, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.APIKeys.SweepRotationGrace.parse-uuid %q: %w", idStr, err)
		}
		out = append(out, persistence.APIKey{ID: u, Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *apiKeysImpl) UpdateLastUsed(ctx context.Context, id shared.UUID, now time.Time, tx persistence.Tx) error {
	_, err := b.run(tx).ExecContext(ctx,
		`UPDATE rimsky_api_keys SET last_used_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339Nano), id.String())
	if err != nil {
		return fmt.Errorf("sqlite.APIKeys.UpdateLastUsed: %w", err)
	}
	return nil
}

func scanOneAPIKey(rows *sql.Rows) (persistence.APIKey, bool, error) {
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
		return persistence.APIKey{}, false, fmt.Errorf("sqlite.APIKeys: unexpected multiple rows")
	}
	if err := rows.Err(); err != nil {
		return persistence.APIKey{}, false, err
	}
	return k, true, nil
}

func scanAPIKeys(rows *sql.Rows) ([]persistence.APIKey, error) {
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

func scanAPIKeyRow(rows *sql.Rows) (persistence.APIKey, error) {
	var (
		k             persistence.APIKey
		idStr         string
		permsStr      string
		createdAtStr  string
		createdByStr  sql.NullString
		lastUsedAtStr sql.NullString
		expiresAtStr  sql.NullString
		revokeAtStr   sql.NullString
		revokedAtStr  sql.NullString
	)
	if err := rows.Scan(
		&idStr, &k.KeyHash, &k.Name, &permsStr, &createdAtStr,
		&createdByStr, &lastUsedAtStr, &expiresAtStr, &revokeAtStr, &revokedAtStr,
	); err != nil {
		return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan: %w", err)
	}
	u, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.id: %w", err)
	}
	k.ID = u
	k.Permissions = []byte(permsStr)
	t, err := parseSQLiteTime(createdAtStr)
	if err != nil {
		return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.created_at: %w", err)
	}
	k.CreatedAt = t
	if createdByStr.Valid {
		cu, err := uuid.Parse(createdByStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.created_by: %w", err)
		}
		k.CreatedByKeyID = &cu
	}
	if lastUsedAtStr.Valid {
		t, err := parseSQLiteTime(lastUsedAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.last_used_at: %w", err)
		}
		k.LastUsedAt = &t
	}
	if expiresAtStr.Valid {
		t, err := parseSQLiteTime(expiresAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.expires_at: %w", err)
		}
		k.ExpiresAt = &t
	}
	if revokeAtStr.Valid {
		t, err := parseSQLiteTime(revokeAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.revoke_at: %w", err)
		}
		k.RevokeAt = &t
	}
	if revokedAtStr.Valid {
		t, err := parseSQLiteTime(revokedAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.revoked_at: %w", err)
		}
		k.RevokedAt = &t
	}
	return k, nil
}

func uuidPtrStr(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return p.String()
}

func timePtrStr(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC().Format(time.RFC3339Nano)
}

// globToLike translates a shell-style glob (`*`, `?`) into the SQL
// LIKE pattern (`%`, `_`), escaping any embedded LIKE wildcards so a
// literal `%` / `_` / `\` in the source name doesn't accidentally
// match more rows than intended. Pair with `LIKE ... ESCAPE '\\'`
// in the query builder.
func globToLike(glob string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(glob)
	return strings.NewReplacer("*", "%", "?", "_").Replace(escaped)
}
