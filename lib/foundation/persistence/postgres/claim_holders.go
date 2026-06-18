// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const claimHolderCols = `id, claim_handle_id, holder_run_id, state, completed_at`

func (s *claimHoldersImpl) Insert(ctx context.Context, in persistence.ClaimHolderInsertInput, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, frame_id)
		 VALUES ($1, $2, $3, 'active', $4)`,
		in.ID, in.ClaimHandleID, in.HolderRunID, in.FrameID,
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Insert: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders WHERE id = $1`, id,
	)
	out, err := scanClaimHolder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *claimHoldersImpl) ListByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = $1
		 ORDER BY id ASC`, claimHandleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListByHolderRun(ctx context.Context, holderRunID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE holder_run_id = $1
		 ORDER BY id ASC`, holderRunID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListActiveByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = $1 AND state = 'active'
		 ORDER BY id ASC`, claimHandleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) Complete(ctx context.Context, id shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = $2,
		        completed_at = now()
		  WHERE id = $1 AND state = 'active'`,
		id, string(state),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Complete: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) CompleteByClaimHandleAndRun(
	ctx context.Context, claimHandleID, holderRunID shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx,
) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = $3,
		        completed_at = now()
		  WHERE claim_handle_id = $1
		    AND holder_run_id = $2
		    AND state = 'active'`,
		claimHandleID, holderRunID, string(state),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.CompleteByClaimHandleAndRun: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) FailAllActiveByClaimHandle(
	ctx context.Context, claimHandleID shared.UUID, supervisorID string, tx persistence.Tx,
) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = 'failed',
		        completed_at = now()
		  WHERE claim_handle_id = $1
		    AND state = 'active'
		    AND EXISTS (
		      SELECT 1 FROM rimsky_claim_handles
		       WHERE id = $1
		         AND `+claimantGuard("", 2)+`
		    )`,
		claimHandleID, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claim_holders.FailAllActiveByClaimHandle: %w", err)
	}
	return nil
}

func scanClaimHolder(sc scannable) (persistence.ClaimHolderRow, error) {
	var (
		r           persistence.ClaimHolderRow
		state       string
		completedAt *time.Time
	)
	if err := sc.Scan(
		&r.ID, &r.ClaimHandleID, &r.HolderRunID,
		&state, &completedAt,
	); err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	r.State = persistence.ClaimHolderState(state)
	r.CompletedAt = completedAt
	return r, nil
}

func collectClaimHolders(rows pgx.Rows) ([]persistence.ClaimHolderRow, error) {
	var out []persistence.ClaimHolderRow
	for rows.Next() {
		r, err := scanClaimHolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
