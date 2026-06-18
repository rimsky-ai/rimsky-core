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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const claimHolderCols = `id, claim_handle_id, holder_run_id, state, completed_at`

func (s *claimHoldersImpl) Insert(ctx context.Context, in persistence.ClaimHolderInsertInput, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, frame_id)
		 VALUES (?, ?, ?, 'active', ?)`,
		in.ID.String(), in.ClaimHandleID.String(), in.HolderRunID.String(), nullableUUID(in.FrameID),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Insert: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHolderRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders WHERE id = ?`, id.String(),
	)
	out, err := scanClaimHolder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *claimHoldersImpl) ListByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = ?
		 ORDER BY id ASC`, claimHandleID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListByHolderRun(ctx context.Context, holderRunID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE holder_run_id = ?
		 ORDER BY id ASC`, holderRunID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListActiveByClaimHandleID(ctx context.Context, claimHandleID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = ? AND state = 'active'
		 ORDER BY id ASC`, claimHandleID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) Complete(ctx context.Context, id shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = ?,
		        completed_at = ?
		  WHERE id = ? AND state = 'active'`,
		string(state), nowUTC(), id.String(),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Complete: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) CompleteByClaimHandleAndRun(
	ctx context.Context, claimHandleID, holderRunID shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = ?,
		        completed_at = ?
		  WHERE claim_handle_id = ?
		    AND holder_run_id = ?
		    AND state = 'active'`,
		string(state), nowUTC(), claimHandleID.String(), holderRunID.String(),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.CompleteByClaimHandleAndRun: %w", err)
	}
	return nil
}

func (s *claimHoldersImpl) FailAllActiveByClaimHandle(
	ctx context.Context, claimHandleID shared.UUID, supervisorID string, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = 'failed',
		        completed_at = ?
		  WHERE claim_handle_id = ?
		    AND state = 'active'
		    AND EXISTS (
		      SELECT 1 FROM rimsky_claim_handles
		       WHERE id = ?
		         AND `+claimantGuardClause+`
		    )`,
		nowUTC(), claimHandleID.String(), claimHandleID.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claim_holders.FailAllActiveByClaimHandle: %w", err)
	}
	return nil
}

func scanClaimHolder(sc scannable) (persistence.ClaimHolderRow, error) {
	var (
		r                persistence.ClaimHolderRow
		idStr            string
		claimHandleIDStr string
		holderRunIDStr   string
		state            string
		completedAtStr   sql.NullString
	)
	if err := sc.Scan(&idStr, &claimHandleIDStr, &holderRunIDStr, &state, &completedAtStr); err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	claimHandleID, err := uuid.Parse(claimHandleIDStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	holderRunID, err := uuid.Parse(holderRunIDStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	r.ID = id
	r.ClaimHandleID = claimHandleID
	r.HolderRunID = holderRunID
	r.State = persistence.ClaimHolderState(state)
	if completedAtStr.Valid {
		t, err := parseTime(completedAtStr.String)
		if err != nil {
			return persistence.ClaimHolderRow{}, err
		}
		r.CompletedAt = &t
	}
	return r, nil
}

func collectClaimHolders(rows *sql.Rows) ([]persistence.ClaimHolderRow, error) {
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
