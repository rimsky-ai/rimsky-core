// insert.go — admin-side bulk insert into the claim-store items table.
//
// Used by the control-api `POST /admin/claim-stores/:name/items` handler
// (spec §7.3) to populate items out-of-band. This is the operator-facing
// path; rimsky itself never enqueues into a claim store.
package claimstorepg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// InsertItems bulk-inserts one row per payload into the items table. Each
// row gets a fresh item_id and `state='available'`; enqueued_at defaults to
// now() via the column default.
//
// Payloads must be valid JSON values; the caller already encoded them as
// json.RawMessage. Empty payload list is a no-op.
//
// Runs against the store's pool (no caller tx); each insert is its own
// statement so a partial failure leaves prior rows committed. Callers that
// require all-or-nothing should pre-validate or wrap in their own
// transaction via the pool directly.
func (s *Store) InsertItems(ctx context.Context, payloads []json.RawMessage) error {
	if len(payloads) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(
		`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`,
		s.itemsTable,
	)
	for i, p := range payloads {
		if len(p) == 0 {
			return fmt.Errorf("claim_store %q: InsertItems: payload at index %d is empty", s.name, i)
		}
		if !json.Valid(p) {
			return fmt.Errorf("claim_store %q: InsertItems: payload at index %d is not valid JSON", s.name, i)
		}
		if _, err := s.pool.Exec(ctx, stmt, uuid.New(), []byte(p)); err != nil {
			return fmt.Errorf("claim_store %q: InsertItems: row %d: %w", s.name, i, err)
		}
	}
	return nil
}
