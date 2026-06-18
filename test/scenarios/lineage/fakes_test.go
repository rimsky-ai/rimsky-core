// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/runtime/lineage_writer_test.go::fakeLineageTable
// @diverged: false
package lineage

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type fakeLineage struct {
	rows []persistence.LineageRow
}

func (f *fakeLineage) Insert(_ context.Context, _ persistence.Tx, row persistence.LineageRow) error {
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeLineage) GetByRunID(_ context.Context, _ shared.UUID) ([]persistence.LineageRow, error) {
	return nil, nil
}

func (f *fakeLineage) GetByClaimHandleID(_ context.Context, _ shared.UUID) ([]persistence.LineageRow, error) {
	return nil, nil
}

func (f *fakeLineage) Query(_ context.Context, _ persistence.LineageQuery, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.LineageRow], error) {
	return persistence.PaginatedListResult[persistence.LineageRow]{}, nil
}

func (f *fakeLineage) QueryByParentRunID(_ context.Context, _ shared.UUID, _ int) ([]persistence.LineageRow, error) {
	return nil, nil
}

func (f *fakeLineage) DeleteOlderThan(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

func (f *fakeLineage) CountOlderThan(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
