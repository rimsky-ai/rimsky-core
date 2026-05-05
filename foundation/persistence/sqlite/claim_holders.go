// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_holders.go — SQLite-backed persistence.ClaimHoldersStore.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

const claimHolderCols = `id, claim_handle_id, holder_node_id, state, completed_at`

func (s *claimHoldersImpl) Insert(ctx context.Context, in persistence.ClaimHolderInsertInput, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_node_id, state, frame_id)
		 VALUES (?, ?, ?, 'active', ?)`,
		in.ID.String(), in.LockHolderID.String(), in.HolderNodeID.String(), nullableUUID(in.FrameID),
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

func (s *claimHoldersImpl) ListByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = ?
		 ORDER BY id ASC`, lockHolderID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE holder_node_id = ?
		 ORDER BY id ASC`, holderNodeID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

func (s *claimHoldersImpl) ListActiveByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_handle_id = ? AND state = 'active'
		 ORDER BY id ASC`, lockHolderID.String(),
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

func (s *claimHoldersImpl) CompleteByLockHolderAndNode(
	ctx context.Context, lockHolderID, holderNodeID shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx,
) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = ?,
		        completed_at = ?
		  WHERE claim_handle_id = ?
		    AND holder_node_id = ?
		    AND state = 'active'`,
		string(state), nowUTC(), lockHolderID.String(), holderNodeID.String(),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.CompleteByLockHolderAndNode: %w", err)
	}
	return nil
}

func scanClaimHolder(sc scannable) (persistence.ClaimHolderRow, error) {
	var (
		r               persistence.ClaimHolderRow
		idStr           string
		lockHolderIDStr string
		holderNodeIDStr string
		state           string
		completedAtStr  sql.NullString
	)
	if err := sc.Scan(&idStr, &lockHolderIDStr, &holderNodeIDStr, &state, &completedAtStr); err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	lockHolderID, err := uuid.Parse(lockHolderIDStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	holderNodeID, err := uuid.Parse(holderNodeIDStr)
	if err != nil {
		return persistence.ClaimHolderRow{}, err
	}
	r.ID = id
	r.LockHolderID = lockHolderID
	r.HolderNodeID = holderNodeID
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
