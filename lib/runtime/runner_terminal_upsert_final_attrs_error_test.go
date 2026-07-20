// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type failingGetByRunNodeAttributesTable struct {
	persistence.NodeAttributeTable
}

func (f *failingGetByRunNodeAttributesTable) GetByRun(
	_ context.Context, _ shared.UUID, _ persistence.Tx,
) (*persistence.NodeAttributesRow, error) {
	return nil, errors.New("injected transient read failure")
}

type faultingNodeAttributesTables struct {
	persistence.Tables
}

func (f *faultingNodeAttributesTables) NodeAttributes() persistence.NodeAttributeTable {
	return &failingGetByRunNodeAttributesTable{NodeAttributeTable: f.Tables.NodeAttributes()}
}

func TestUpsertFinalAttributesTx_PropagatesPriorReadError(t *testing.T) {
	t.Parallel()
	baseArgs, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.NodeAttributes().Upsert(ctx, acq.NodeRunID, acq.NodeID,
			map[string]any{"kept": "from-prior"}, tx)
	}))

	args := baseArgs
	args.Persist = &faultingNodeAttributesTables{Tables: tables}

	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return upsertFinalAttributesTx(ctx, args, tx, acq, map[string]any{"new": "value"})
	})
	require.Error(t, err,
		"a transient GetByRun read failure must fail the transaction instead of silently dropping "+
			"the run's previously persisted attribute data from the final merge")
	require.ErrorContains(t, err, "injected transient read failure")

	var bag *persistence.NodeAttributesRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := tables.NodeAttributes().GetByRun(ctx, acq.NodeRunID, tx)
		bag = row
		return err
	}))
	require.NotNil(t, bag)
	require.Equal(t, "from-prior", bag.Data["kept"],
		"the failed upsert must not have overwritten the prior row with a partial merge")
	require.NotContains(t, bag.Data, "new")
}
