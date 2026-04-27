package claimstorepg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/store"
)

// AcquireLock performs the atomic items-table flip described in spec §13.3:
// SELECT the next available row FOR UPDATE SKIP LOCKED, UPDATE its state
// to 'in_progress' with a fresh claim_token, RETURNING item_id and payload.
//
// Reads the open *pgx.Tx from `store.TxFromContext`. The store mutates
// inside the supervisor's outer acquisition tx so the items-table flip,
// the rimsky_lock_holders insert, and the dispatch claim either all
// commit or all roll back together.
//
// Returns an empty ClaimResult when no row is available. The supervisor
// treats that as an "ineligible at the last moment" outcome and rolls
// back the outer tx (spec §13.3 step 3e).
func (s *Store) AcquireLock(ctx context.Context, spec store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
	if _, ok := spec.(store.ClaimLockSpec); !ok {
		return store.LockHandle{}, store.ClaimResult{}, fmt.Errorf("claim_store %q: AcquireLock requires ClaimLockSpec, got %T", s.name, spec)
	}
	// v1 ignores spec.Criteria — see HasClaimableItem comment. Kept on the
	// lock spec for the future criteria-derived predicate path described in
	// spec §13.3 ("(<criteria-derived predicate> OR true)").

	tx, ok := store.TxFromContext(ctx)
	if !ok {
		return store.LockHandle{}, store.ClaimResult{}, fmt.Errorf("claim_store %q: AcquireLock requires an open pgx.Tx via store.WithTx (see spec §13.3)", s.name)
	}

	// Avoid storing a per-process counter — generate a fresh claim_token
	// per acquisition. The supervisor doesn't read the token itself; it's
	// part of the §9.10 contract for the items table so external observers
	// can correlate in-progress rows with whatever produced them.
	token, err := uuid.NewRandom()
	if err != nil {
		return store.LockHandle{}, store.ClaimResult{}, fmt.Errorf("claim_store %q: generate claim_token: %w", s.name, err)
	}

	// v1 ignores cls.Criteria (see HasClaimableItem comment); the SQL is
	// the literal §13.3 shape with the criteria predicate elided.
	q := fmt.Sprintf(`UPDATE %s
		   SET state = 'in_progress', claim_token = $1, claimed_at = now()
		 WHERE item_id = (
		       SELECT item_id FROM %s
		        WHERE state = 'available'
		        ORDER BY enqueued_at
		          FOR UPDATE SKIP LOCKED
		        LIMIT 1
		       )
		 RETURNING item_id, payload`,
		s.itemsTable, s.itemsTable,
	)

	row := tx.QueryRow(ctx, q, token)
	var (
		itemID  uuid.UUID
		rawJSON []byte
	)
	if err := row.Scan(&itemID, &rawJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Pool empty — supervisor rolls back outer tx and tries the
			// next dispatch row. This is not an error condition.
			return store.LockHandle{}, store.ClaimResult{}, nil
		}
		return store.LockHandle{}, store.ClaimResult{}, fmt.Errorf("claim_store %q: claim-pick: %w", s.name, err)
	}

	// Decode the JSONB payload into a generic any so the executor handle
	// can carry an arbitrary user-data shape. We do not validate against
	// any schema here — payload semantics are user-defined per spec §7.1.
	var payload any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		return store.LockHandle{}, store.ClaimResult{}, fmt.Errorf("claim_store %q: decode payload jsonb: %w", s.name, err)
	}

	return store.LockHandle{}, store.ClaimResult{
		Payload:        payload,
		ClaimID:        itemID.String(),
		ResolvedRegion: nil, // claim stores have no regions
	}, nil
}
