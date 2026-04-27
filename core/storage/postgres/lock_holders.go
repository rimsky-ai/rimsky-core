// Adapter that exposes the storage.LockHoldersStore surface on top of the
// concrete *store.LockHoldersClient implementation. The adapter does no
// SQL of its own — it converts between the two row shapes
// (storage.LockHolderRow vs store.LockHolderRow, identical field-for-field
// modulo packaging) and unwraps storage.Tx → pgx.Tx for transactional
// methods.
//
// The split exists because core/store/lockholders.go cannot import
// core/storage (per the package layout rule on Task 11). The actual
// helpers — including the §13.4 heartbeat SQL with the running-node
// filter — live in core/store/lockholders.go; this file is the storage
// surface adapter.
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
	"github.com/fallguy/rimsky/core/store"
)

// LockHoldersStore is the storage-package adapter satisfying
// storage.LockHoldersStore. It delegates to a *store.LockHoldersClient.
type LockHoldersStore struct {
	pool   *pgxpool.Pool
	client *store.LockHoldersClient
}

var _ storage.LockHoldersStore = (*LockHoldersStore)(nil)

// Client returns the underlying core/store-layer helper. Callers in the
// scheduler / supervisor that need the helpers not exposed by
// storage.LockHoldersStore (RefreshHeartbeat, ListByNodeAndStore,
// RebindForResume, etc.) reach for the client directly.
func (s *LockHoldersStore) Client() *store.LockHoldersClient { return s.client }

// Insert satisfies storage.LockHoldersStore.
func (s *LockHoldersStore) Insert(ctx context.Context, in storage.LockHolderInsertInput, tx storage.Tx) error {
	pgT, err := pgxTxFromStorage(tx)
	if err != nil {
		return err
	}
	if pgT == nil {
		// LockHolders insert MUST commit atomically with the dispatch
		// claim; absence of a tx is a programming error.
		return errors.New("lockholders.Insert: storage.Tx required")
	}
	now := time.Now().UTC()
	row := store.LockHolderRow{
		ID:                 in.ID,
		Kind:               store.LockHolderKind(in.LockKind),
		LockName:           in.LockName,
		StoreName:          in.StoreName,
		RegionData:         in.RegionData,
		ClaimID:            in.ClaimID,
		HolderSupervisorID: in.HolderSupervisorID,
		HolderNodeID:       in.HolderNodeID,
		ClaimedAt:          now,
		LastHeartbeatAt:    now,
		ExpiresAt:          in.ExpiresAt,
	}
	return s.client.Insert(ctx, pgT, row)
}

// Get satisfies storage.LockHoldersStore.
func (s *LockHoldersStore) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.LockHolderRow, error) {
	row, err := s.client.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	out := storeRowToStorageRow(*row)
	return &out, nil
}

// ListByHolderNode satisfies storage.LockHoldersStore.
func (s *LockHoldersStore) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx storage.Tx) ([]storage.LockHolderRow, error) {
	in, err := s.client.ListByHolderNode(ctx, holderNodeID)
	if err != nil {
		return nil, err
	}
	return storeRowsToStorageRows(in), nil
}

// ListBySupervisor satisfies storage.LockHoldersStore.
func (s *LockHoldersStore) ListBySupervisor(ctx context.Context, supervisorID string, tx storage.Tx) ([]storage.LockHolderRow, error) {
	in, err := s.client.ListBySupervisor(ctx, supervisorID)
	if err != nil {
		return nil, err
	}
	return storeRowsToStorageRows(in), nil
}

// ExtendHeartbeat satisfies storage.LockHoldersStore. The set of
// running nodes is determined by the underlying SQL itself — the
// UPDATE filters on `holder_node_id IN (running nodes)` directly via
// subquery (per spec §13.4). The supervisor does not pass a list of
// running node IDs because the SQL is the single source of truth for
// which rows are eligible for the heartbeat refresh.
func (s *LockHoldersStore) ExtendHeartbeat(
	ctx context.Context, supervisorID string, expiresAt time.Time, tx storage.Tx,
) error {
	pgT, err := pgxTxFromStorage(tx)
	if err != nil {
		return err
	}
	// Convert expiresAt offset into an integer-second budget so we can
	// reuse the §13.4 SQL verbatim (which expresses the bound as
	// `now() + (N * interval '1 second')`).
	heartbeatSeconds := int(time.Until(expiresAt).Seconds())
	if heartbeatSeconds < 1 {
		heartbeatSeconds = 1
	}
	if pgT == nil {
		return s.client.RefreshHeartbeat(ctx, supervisorID, heartbeatSeconds)
	}
	return s.client.ExtendHeartbeatForRunningNodes(ctx, pgT, supervisorID, heartbeatSeconds)
}

// ListExpired satisfies storage.LockHoldersStore.
func (s *LockHoldersStore) ListExpired(ctx context.Context, tx storage.Tx) ([]storage.LockHolderRow, error) {
	in, err := s.client.ListExpired(ctx)
	if err != nil {
		return nil, err
	}
	return storeRowsToStorageRows(in), nil
}

// Delete satisfies storage.LockHoldersStore. Claimant-guarded on
// supervisor_id; mismatch is a no-op (returns nil). A non-nil tx is
// required: per spec §13.5 step 2 the lock-holder row deletion must
// commit atomically with the store-side `ReleaseLock(give_up)` (so the
// items-table flip and the lock-holder delete become visible together).
// The orphan-reap caller holds the outer tx and threads it here.
func (s *LockHoldersStore) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx storage.Tx) error {
	pgT, err := pgxTxFromStorage(tx)
	if err != nil {
		return err
	}
	if pgT == nil {
		return errors.New("lockholders.Delete: storage.Tx required (must commit with store-side ReleaseLock per §13.5)")
	}
	return s.client.DeleteByID(ctx, pgT, id, expectedSupervisorID)
}

// ---- conversion helpers ----

func storeRowToStorageRow(in store.LockHolderRow) storage.LockHolderRow {
	return storage.LockHolderRow{
		ID:                 in.ID,
		LockKind:           storage.LockKind(in.Kind),
		LockName:           in.LockName,
		StoreName:          in.StoreName,
		RegionData:         in.RegionData,
		ClaimID:            in.ClaimID,
		HolderSupervisorID: in.HolderSupervisorID,
		HolderNodeID:       in.HolderNodeID,
		ClaimedAt:          in.ClaimedAt,
		LastHeartbeatAt:    in.LastHeartbeatAt,
		ExpiresAt:          in.ExpiresAt,
	}
}

func storeRowsToStorageRows(in []store.LockHolderRow) []storage.LockHolderRow {
	if len(in) == 0 {
		return nil
	}
	out := make([]storage.LockHolderRow, len(in))
	for i, r := range in {
		out[i] = storeRowToStorageRow(r)
	}
	return out
}

// pgxTxFromStorage unwraps a storage.Tx to a pgx.Tx via the local *pgTx
// carrier. Returns (nil, nil) when tx is nil.
func pgxTxFromStorage(tx storage.Tx) (pgx.Tx, error) {
	if tx == nil {
		return nil, nil
	}
	carrier, ok := tx.(*pgTx)
	if !ok {
		return nil, fmt.Errorf("storage.Tx: unexpected concrete type %T", tx)
	}
	return carrier.tx, nil
}
