// ClaimHoldersStore is the postgres accessor for `rimsky_claim_holders`
// (spec §9.9.3). One row per (claim_id, terminal-leaf-node) pair, inserted
// at commit of the claiming-source node. Rows transition `'active'` →
// `'completed'` per the §5.6.4 resolution algorithm.
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

const claimHolderCols = `
  id, claim_id, store_name, holder_node_id, on_commit, on_give_up,
  actual_action, state, created_at, completed_at
`

// Insert satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) Insert(ctx context.Context, in storage.ClaimHolderInsertInput, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_claim_holders (
		   id, claim_id, store_name, holder_node_id,
		   on_commit, on_give_up, state, frame_id
		 ) VALUES ($1, $2, $3, $4, $5, $6, 'active', $7)`,
		in.ID, in.ClaimID, in.StoreName, in.HolderNodeID,
		string(in.OnCommit), string(in.OnGiveUp), in.FrameID,
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

// ListByClaimID satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) ListByClaimID(ctx context.Context, claimID string, tx storage.Tx) ([]storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_id = $1
		 ORDER BY created_at ASC`, claimID,
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
		 ORDER BY created_at ASC`, holderNodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

// ListActiveByClaimID satisfies storage.ClaimHoldersStore.
func (s *ClaimHoldersStore) ListActiveByClaimID(ctx context.Context, claimID string, tx storage.Tx) ([]storage.ClaimHolderRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+claimHolderCols+` FROM rimsky_claim_holders
		 WHERE claim_id = $1 AND state = 'active'
		 ORDER BY created_at ASC`, claimID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClaimHolders(rows)
}

// Complete satisfies storage.ClaimHoldersStore. Idempotent: re-running
// Complete on an already-completed row is a no-op (the WHERE clause
// filters on state='active').
func (s *ClaimHoldersStore) Complete(ctx context.Context, id shared.UUID, actualAction storage.ClaimHolderAction, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_claim_holders
		    SET state = 'completed',
		        completed_at = now(),
		        actual_action = $2
		  WHERE id = $1 AND state = 'active'`,
		id, string(actualAction),
	)
	if err != nil {
		return fmt.Errorf("claim_holders.Complete: %w", err)
	}
	return nil
}

// ---- helpers ----

func scanClaimHolder(sc scannable) (storage.ClaimHolderRow, error) {
	var (
		r            storage.ClaimHolderRow
		actualAction *string
		state        string
		completedAt  *time.Time
	)
	if err := sc.Scan(
		&r.ID, &r.ClaimID, &r.StoreName, &r.HolderNodeID,
		&r.OnCommit, &r.OnGiveUp,
		&actualAction, &state,
		&r.CreatedAt, &completedAt,
	); err != nil {
		return storage.ClaimHolderRow{}, err
	}
	r.State = storage.ClaimHolderState(state)
	if actualAction != nil {
		a := storage.ClaimHolderAction(*actualAction)
		r.ActualAction = &a
	}
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
