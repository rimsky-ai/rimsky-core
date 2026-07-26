// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type deploymentCAImpl tablesImpl

var _ persistence.DeploymentCATable = (*deploymentCAImpl)(nil)

func (s *tablesImpl) DeploymentCA() persistence.DeploymentCATable { return (*deploymentCAImpl)(s) }

func (b *deploymentCAImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).db
	}
	return (*tablesImpl)(b).q(tx)
}

const sqliteSelectDeploymentCASQL = `
	SELECT id, ca_cert_pem, ca_key_encrypted, created_at
	  FROM rimsky_deployment_ca
	 WHERE id = ?`

func (b *deploymentCAImpl) Get(ctx context.Context, tx persistence.Tx) (persistence.DeploymentCA, bool, error) {
	rows, err := b.run(tx).QueryContext(ctx, sqliteSelectDeploymentCASQL, persistence.DeploymentCASingletonID.String())
	if err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("sqlite.DeploymentCA.Get: %w", err)
	}
	defer rows.Close()
	return scanOneDeploymentCA(rows)
}

const sqliteInsertDeploymentCASQL = `
	INSERT OR IGNORE INTO rimsky_deployment_ca (id, ca_cert_pem, ca_key_encrypted, created_at)
	VALUES (?, ?, ?, ?)`

func (b *deploymentCAImpl) GetOrCreate(ctx context.Context, candidate persistence.DeploymentCA, tx persistence.Tx) (persistence.DeploymentCA, error) {
	if tx == nil {
		var out persistence.DeploymentCA
		err := (*tablesImpl)(b).Transaction(ctx, func(ctx context.Context, itx persistence.Tx) error {
			var ierr error
			out, ierr = b.GetOrCreate(ctx, candidate, itx)
			return ierr
		})
		return out, err
	}
	createdAt := candidate.CreatedAt.UTC().Format(timeLayoutFixedNanos)
	if _, err := b.run(tx).ExecContext(ctx, sqliteInsertDeploymentCASQL,
		persistence.DeploymentCASingletonID.String(), candidate.CACertPEM, candidate.CAKeyEncrypted, createdAt,
	); err != nil {
		return persistence.DeploymentCA{}, fmt.Errorf("sqlite.DeploymentCA.GetOrCreate.insert: %w", err)
	}
	row, ok, err := b.Get(ctx, tx)
	if err != nil {
		return persistence.DeploymentCA{}, err
	}
	if !ok {
		return persistence.DeploymentCA{}, errors.New("sqlite.DeploymentCA.GetOrCreate: row missing after insert")
	}
	return row, nil
}

func scanOneDeploymentCA(rows *sql.Rows) (persistence.DeploymentCA, bool, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return persistence.DeploymentCA{}, false, err
		}
		return persistence.DeploymentCA{}, false, nil
	}
	var (
		ca           persistence.DeploymentCA
		idStr        string
		createdAtStr string
	)
	if err := rows.Scan(&idStr, &ca.CACertPEM, &ca.CAKeyEncrypted, &createdAtStr); err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("sqlite.DeploymentCA.scan: %w", err)
	}
	u, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("sqlite.DeploymentCA.scan.id: %w", err)
	}
	ca.ID = u
	t, err := parseTime(createdAtStr)
	if err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("sqlite.DeploymentCA.scan.created_at: %w", err)
	}
	ca.CreatedAt = t
	if rows.Next() {
		return persistence.DeploymentCA{}, false, fmt.Errorf("sqlite.DeploymentCA: unexpected multiple rows")
	}
	if err := rows.Err(); err != nil {
		return persistence.DeploymentCA{}, false, err
	}
	return ca, true, nil
}
