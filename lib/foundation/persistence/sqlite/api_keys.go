// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: api-key

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type apiKeysImpl tablesImpl

var _ persistence.APIKeyTable = (*apiKeysImpl)(nil)

func (s *tablesImpl) APIKeys() persistence.APIKeyTable { return (*apiKeysImpl)(s) }

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
	createdAt := k.CreatedAt.UTC().Format(timeLayoutFixedNanos)
	_, err := b.run(tx).ExecContext(ctx, sqliteInsertAPIKeySQL,
		k.ID.String(), k.KeyHash, k.Name, string(perms), createdAt,
		uuidPtrStr(k.CreatedByKeyID),
		timePtrStr(k.ExpiresAt),
		timePtrStr(k.RevokeAt),
		timePtrStr(k.RevokedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
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
	nowStr := now.UTC().Format(timeLayoutFixedNanos)
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
	if tx == nil {
		txErr := (*tablesImpl)(b).Transaction(ctx, func(ctx context.Context, itx persistence.Tx) error {
			var ierr error
			changed, found, ierr = b.markRevokedInTx(ctx, id, now, itx)
			return ierr
		})
		return changed, found, txErr
	}
	return b.markRevokedInTx(ctx, id, now, tx)
}

func (b *apiKeysImpl) markRevokedInTx(ctx context.Context, id shared.UUID, now time.Time, tx persistence.Tx) (changed bool, found bool, err error) {
	nowStr := now.UTC().Format(timeLayoutFixedNanos)
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
	var exists int
	if err := b.run(tx).QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM rimsky_api_keys WHERE id = ?)`, id.String()).Scan(&exists); err != nil {
		return false, false, fmt.Errorf("sqlite.APIKeys.MarkRevoked.exists: %w", err)
	}
	return false, exists != 0, nil
}

func (b *apiKeysImpl) RevokeIfNotLast(ctx context.Context, id shared.UUID, now time.Time, force bool, tx persistence.Tx) (persistence.RevokeResult, error) {
	if tx == nil {
		var result persistence.RevokeResult
		txErr := (*tablesImpl)(b).Transaction(ctx, func(ctx context.Context, itx persistence.Tx) error {
			var ierr error
			result, ierr = b.revokeIfNotLastInTx(ctx, id, now, force, itx)
			return ierr
		})
		return result, txErr
	}
	return b.revokeIfNotLastInTx(ctx, id, now, force, tx)
}

func (b *apiKeysImpl) revokeIfNotLastInTx(ctx context.Context, id shared.UUID, now time.Time, force bool, tx persistence.Tx) (persistence.RevokeResult, error) {
	nowStr := now.UTC().Format(timeLayoutFixedNanos)
	if !force {
		var targetActive int
		if err := b.run(tx).QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rimsky_api_keys
			  WHERE id = ?
			    AND revoked_at IS NULL
			    AND (expires_at IS NULL OR expires_at > ?)
			    AND (revoke_at IS NULL OR revoke_at > ?)`, id.String(), nowStr, nowStr).Scan(&targetActive); err != nil {
			return 0, fmt.Errorf("sqlite.APIKeys.RevokeIfNotLast.targetActive: %w", err)
		}
		if targetActive > 0 {
			var active int
			if err := b.run(tx).QueryRowContext(ctx,
				`SELECT COUNT(*) FROM rimsky_api_keys
				  WHERE revoked_at IS NULL
				    AND (expires_at IS NULL OR expires_at > ?)
				    AND (revoke_at IS NULL OR revoke_at > ?)`, nowStr, nowStr).Scan(&active); err != nil {
				return 0, fmt.Errorf("sqlite.APIKeys.RevokeIfNotLast.activeCount: %w", err)
			}
			if active <= 1 {
				return persistence.RevokeResultWouldLeaveNoneActive, nil
			}
		}
	}
	res, err := b.run(tx).ExecContext(ctx,
		`UPDATE rimsky_api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		nowStr, id.String())
	if err != nil {
		return 0, fmt.Errorf("sqlite.APIKeys.RevokeIfNotLast.update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return persistence.RevokeResultRevoked, nil
	}
	var exists int
	if err := b.run(tx).QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM rimsky_api_keys WHERE id = ?)`, id.String()).Scan(&exists); err != nil {
		return 0, fmt.Errorf("sqlite.APIKeys.RevokeIfNotLast.exists: %w", err)
	}
	if exists == 0 {
		return persistence.RevokeResultNotFound, nil
	}
	return persistence.RevokeResultAlreadyRevoked, nil
}

func (b *apiKeysImpl) SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time, tx persistence.Tx) error {
	_, err := b.run(tx).ExecContext(ctx,
		`UPDATE rimsky_api_keys SET revoke_at = ? WHERE id = ?`,
		at.UTC().Format(timeLayoutFixedNanos), id.String())
	if err != nil {
		return fmt.Errorf("sqlite.APIKeys.SetRevokeAt: %w", err)
	}
	return nil
}

func (b *apiKeysImpl) SweepRotationGrace(ctx context.Context, now time.Time, tx persistence.Tx) ([]persistence.APIKey, error) {
	nowStr := now.UTC().Format(timeLayoutFixedNanos)
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
		now.UTC().Format(timeLayoutFixedNanos), id.String())
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
	t, err := parseTime(createdAtStr)
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
		t, err := parseTime(lastUsedAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.last_used_at: %w", err)
		}
		k.LastUsedAt = &t
	}
	if expiresAtStr.Valid {
		t, err := parseTime(expiresAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.expires_at: %w", err)
		}
		k.ExpiresAt = &t
	}
	if revokeAtStr.Valid {
		t, err := parseTime(revokeAtStr.String)
		if err != nil {
			return persistence.APIKey{}, fmt.Errorf("sqlite.APIKeys.scan.revoke_at: %w", err)
		}
		k.RevokeAt = &t
	}
	if revokedAtStr.Valid {
		t, err := parseTime(revokedAtStr.String)
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
	return p.UTC().Format(timeLayoutFixedNanos)
}

func globToLike(glob string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(glob)
	return strings.NewReplacer("*", "%", "?", "_").Replace(escaped)
}
