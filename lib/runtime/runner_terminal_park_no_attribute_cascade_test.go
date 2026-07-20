// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestApplyTerminalPark_DoesNotEmitAttributeChangedCascade(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.NodeAttributes().Upsert(ctx, acq.NodeRunID, acq.NodeID,
			map[string]any{"foo": "bar"}, tx)
	}))

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalPark(ctx, args, acq, terminalEvent{
			Kind:         terminalKindPark,
			ParkResumeAt: time.Now().Add(time.Hour),
		}, tx)
		return err
	}))

	require.Equal(t, 0, countSignalAudits(t, tables, acq.NodeID, "attribute/foo/changed"),
		"park is dispatch-internal and writes no attributes; it must not fire attribute/<key>/changed "+
			"cascade signals (those diffs would double-emit when the run later actually settles)")
}
