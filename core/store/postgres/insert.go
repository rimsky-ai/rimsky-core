// insert.go — admin-side bulk insert into a configured pick policy's
// items table. Used by the control-api admin handler to populate items
// out-of-band; rimsky itself never enqueues.

package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// InsertItems bulk-inserts one row per payload into the items table for
// the named pick policy. Each row gets a fresh item_id and
// state='available'; enqueued_at defaults to now() via the column
// default; priority defaults to 0; sequence is assigned by BIGSERIAL.
//
// selector is the configured pick-policy key (e.g. "@review-queue").
// Returns an error if the selector is unknown.
//
// Payloads must be valid JSON values. Empty payload list is a no-op.
//
// Runs against the store's pool (no caller tx); each insert is its own
// statement so a partial failure leaves prior rows committed.
func (s *Store) InsertItems(ctx context.Context, selector string, payloads []json.RawMessage) error {
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return fmt.Errorf("postgres store %q: InsertItems: no pick policy configured for selector %q", s.name, selector)
	}
	if len(payloads) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(
		`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`,
		pp.itemsTable,
	)
	for i, p := range payloads {
		if len(p) == 0 {
			return fmt.Errorf("postgres store %q: InsertItems: payload at index %d is empty", s.name, i)
		}
		if !json.Valid(p) {
			return fmt.Errorf("postgres store %q: InsertItems: payload at index %d is not valid JSON", s.name, i)
		}
		if _, err := s.pool.Exec(ctx, stmt, uuid.New().String(), []byte(p)); err != nil {
			return fmt.Errorf("postgres store %q: InsertItems: row %d: %w", s.name, i, err)
		}
	}
	return nil
}
