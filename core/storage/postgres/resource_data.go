// ResourceDataStore — inline-JSONB implementation. Port of
// rimsky/src/storage/postgres/resource-data-store.ts.
//
// The `data` column of rimsky_resource_versions holds the JSON payload;
// commitVersion in ResourceRegistry writes it during INSERT, so this backend
// is read + delete only.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
)

type ResourceDataStore struct {
	pool *pgxpool.Pool
}

var _ storage.ResourceDataStore = (*ResourceDataStore)(nil)

func (s *ResourceDataStore) Read(ctx context.Context, version storage.ResourceVersionRow, tx storage.Tx) (any, error) {
	_ = ctx
	_ = tx
	if len(version.Data) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(version.Data, &out); err != nil {
		return nil, fmt.Errorf("resourceData.read: unmarshal: %w", err)
	}
	return out, nil
}

func (s *ResourceDataStore) Delete(ctx context.Context, version storage.ResourceVersionRow, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`DELETE FROM rimsky_resource_versions WHERE id = $1`, version.ID)
	return err
}
