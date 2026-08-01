// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type deploymentCAImpl tablesImpl

var _ persistence.DeploymentCATable = (*deploymentCAImpl)(nil)

func (s *tablesImpl) DeploymentCA() persistence.DeploymentCATable { return (*deploymentCAImpl)(s) }

func (b *deploymentCAImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).pool
	}
	return (*tablesImpl)(b).q(tx)
}

const selectDeploymentCASQL = `
	SELECT id, ca_cert_pem, ca_key_encrypted, created_at
	  FROM rimsky_deployment_ca
	 WHERE id = $1`

func (b *deploymentCAImpl) Get(ctx context.Context, tx persistence.Tx) (persistence.DeploymentCA, bool, error) {
	rows, err := b.run(tx).Query(ctx, selectDeploymentCASQL, persistence.DeploymentCASingletonID)
	if err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("postgres.DeploymentCA.Get: %w", err)
	}
	defer rows.Close()
	return scanOneDeploymentCA(rows)
}

const insertDeploymentCASQL = `
	INSERT INTO rimsky_deployment_ca (id, ca_cert_pem, ca_key_encrypted, created_at)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (id) DO NOTHING`

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
	candidate.ID = persistence.DeploymentCASingletonID
	if _, err := b.run(tx).Exec(ctx, insertDeploymentCASQL,
		candidate.ID, candidate.CACertPEM, candidate.CAKeyEncrypted, candidate.CreatedAt,
	); err != nil {
		return persistence.DeploymentCA{}, fmt.Errorf("postgres.DeploymentCA.GetOrCreate.insert: %w", err)
	}
	row, ok, err := b.Get(ctx, tx)
	if err != nil {
		return persistence.DeploymentCA{}, err
	}
	if !ok {
		return persistence.DeploymentCA{}, errors.New("postgres.DeploymentCA.GetOrCreate: row missing after insert")
	}
	return row, nil
}

func scanOneDeploymentCA(rows pgx.Rows) (persistence.DeploymentCA, bool, error) {
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return persistence.DeploymentCA{}, false, err
		}
		return persistence.DeploymentCA{}, false, nil
	}
	var ca persistence.DeploymentCA
	if err := rows.Scan(&ca.ID, &ca.CACertPEM, &ca.CAKeyEncrypted, &ca.CreatedAt); err != nil {
		return persistence.DeploymentCA{}, false, fmt.Errorf("postgres.DeploymentCA.scan: %w", err)
	}
	if rows.Next() {
		return persistence.DeploymentCA{}, false, fmt.Errorf("postgres.DeploymentCA: unexpected multiple rows")
	}
	if err := rows.Err(); err != nil {
		return persistence.DeploymentCA{}, false, err
	}
	return ca, true, nil
}
