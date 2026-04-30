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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// ClaimHoldersStore is the postgres ClaimHoldersStore implementation.
type ClaimHoldersStore struct {
	pool *pgxpool.Pool
}

var _ storage.ClaimHoldersStore = (*ClaimHoldersStore)(nil)

const claimHolderCols = `id, lock_holder_id, holder_node_id, state, completed_at`

// Insert satisfies storage.ClaimHoldersStore. Rows are inserted in
// 'active' state.
func (s *ClaimHoldersStore) Insert(ctx context.Context, in storage.ClaimHolderInsertInput, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

// Get satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
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

// ListByLockHolderID satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) ListByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx storage.Tx) ([]storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
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

// ListByHolderNode satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx storage.Tx) ([]storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
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

// ListActiveByLockHolderID satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) ListActiveByLockHolderID(ctx context.Context, lockHolderID shared.UUID, tx storage.Tx) ([]storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
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

// Complete satisfies storage.ClaimHoldersStore. Idempotent: only updates
// rows still in 'active' state.
func (s *ClaimHoldersStore) Complete(ctx context.Context, id shared.UUID, state storage.ClaimHolderState, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

// CompleteByLockHolderAndNode satisfies storage.ClaimHoldersStore. Single
// targeted UPDATE on the unique (lock_holder_id, holder_node_id) pair —
// avoids the read-then-write round-trip the supervisor's terminal-release
// path would otherwise pay per held alias.
func (s *ClaimHoldersStore) CompleteByLockHolderAndNode(
	ctx context.Context, lockHolderID, holderNodeID shared.UUID, state storage.ClaimHolderState, tx storage.Tx,
) error {
	ex := q(tx, s.pool)
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

func scanClaimHolder(sc scannable) (storage.ClaimHolderRow, error) {
	var (
		r           storage.ClaimHolderRow
		state       string
		completedAt *time.Time
	)
	if err := sc.Scan(
		&r.ID, &r.LockHolderID, &r.HolderNodeID,
		&state, &completedAt,
	); err != nil {
		return storage.ClaimHolderRow{}, err
	}
	r.State = storage.ClaimHolderState(state)
	r.CompletedAt = completedAt
	return r, nil
}

func collectClaimHolders(rows pgx.Rows) ([]storage.ClaimHolderRow, error) {
	var out []storage.ClaimHolderRow
	for rows.Next() {
		r, err := scanClaimHolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
