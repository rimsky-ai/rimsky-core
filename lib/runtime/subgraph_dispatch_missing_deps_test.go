// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestApplyTerminalCompleteSubgraphCaller_MissingInstanceErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	acq := &acquisition{
		NodeRunID:  shared.UUID(uuid.New()),
		NodeID:     shared.UUID(uuid.New()),
		InstanceID: shared.UUID(uuid.New()),
		NodeType:   "outer-caller",
		NodeDef:    &node.TemplateNodeDef{Type: "outer-caller", Delegate: "staging"},
	}
	args := RunArgs{
		Persist:      backend,
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-missing-instance",
	}
	err := backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalCompleteSubgraphCaller(ctx, args, acq, map[string]any{},
			terminalEvent{Kind: terminalKindComplete}, tx)
		return err
	})
	require.Error(t, err,
		"a subgraph caller whose instance row is gone must fail the terminal instead of "+
			"silently marking the caller running with zero dispatched children")
	require.ErrorContains(t, err, "instance")
}
