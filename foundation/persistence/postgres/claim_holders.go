// ClaimHoldersStore is the postgres accessor for `rimsky_claim_holders`.
// One row per (lock_holder, holder_node) pair from the §18.4 holding
// subgraph. Rows transition `'active'` → `'completed'` (success) or
// `'failed'` (give-up/failure) per §4.10 invariant 13. The lock_holder_id FK cascades
// deletes when the parent rimsky_lock_holders row is removed at
// auto-terminal.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// ClaimHoldersStore is the postgres ClaimHoldersStore implementation.

const claimHolderCols = `id, lock_holder_id, holder_node_id, state, completed_at`

// Insert satisfies persistence.ClaimHoldersStore. Rows are inserted in
// 'active' state.
func (s *claimHoldersImpl) Insert(ctx context.Context, in persistence.ClaimHolderInsertInput, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_claim_holders (id, lock_holder_id, holder_node_id, state, frame_id)
		 VALUES ($1, $2, $3, 'active', $4)`,
		in.ID, in.LockHolderID, in.HolderNodeID, in.FrameID,
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Insert: %w", err)
	}
	return nil
}

// Get satisfies persistence.ClaimHoldersStore.
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

// ListByLockHolderID satisfies persistence.ClaimHoldersStore.
func (s *claimHoldersImpl) ListByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE lock_holder_id = $1
		 ORDER BY id ASC`, lockHolderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

// ListByHolderNode satisfies persistence.ClaimHoldersStore.
func (s *claimHoldersImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE holder_node_id = $1
		 ORDER BY id ASC`, holderNodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

// ListActiveByLockHolderID satisfies persistence.ClaimHoldersStore.
func (s *claimHoldersImpl) ListActiveByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHolderRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE lock_holder_id = $1 AND state = 'active'
		 ORDER BY id ASC`, lockHolderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

// Complete satisfies persistence.ClaimHoldersStore. Idempotent: only updates
// rows still in 'active' state.
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

// CompleteByLockHolderAndNode satisfies persistence.ClaimHoldersStore. Single
// targeted UPDATE on the unique (lock_holder_id, holder_node_id) pair —
// avoids the read-then-write round-trip the supervisor's terminal-release
// path would otherwise pay per held alias.
func (s *claimHoldersImpl) CompleteByLockHolderAndNode(
	ctx context.Context, lockHolderID, holderNodeID shared.UUID, state persistence.ClaimHolderState, tx persistence.Tx,
) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = $3,
		        completed_at = now()
		  WHERE lock_holder_id = $1
		    AND holder_node_id = $2
		    AND state = 'active'`,
		lockHolderID, holderNodeID, string(state),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.CompleteByLockHolderAndNode: %w", err)
	}
	return nil
}

// ---- helpers ----

func scanClaimHolder(sc scannable) (persistence.ClaimHolderRow, error) {
	var (
		r           persistence.ClaimHolderRow
		state       string
		completedAt *time.Time
	)
	if err := sc.Scan(
		&r.ID, &r.LockHolderID, &r.HolderNodeID,
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
