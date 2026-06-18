// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHitQueueOverflowDropOldest(t *testing.T) {
	t.Parallel()
	testStart := time.Now()
	testBudget := 30 * time.Second
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-overflow-drop-oldest", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-overflow-drop-oldest", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":      "before_dispatch",
		"mode":            "notify_only",
		"overflow_policy": "drop_oldest",
	})

	args := runtime.RunArgs{Persist: h.Persist, Logger: shared.SilentLogger{}}
	cc := runtime.CheckpointContext{
		InstanceID:       iid,
		DispatchID:       shared.UUID(uuid.New()),
		FrameID:          shared.UUID(uuid.New()),
		Executor:         "stub",
		NodeType:         "worker",
		Graph:            "main",
		ChildKey:         "",
		MergedAttributes: map[string]any{"k": "v"},
		Checkpoint:       persistence.CheckpointBeforeDispatch,
	}
	const total = 150
	for i := 0; i < total; i++ {
		cc.DispatchID = shared.UUID(uuid.New())
		cc.FrameID = shared.UUID(uuid.New())
		_, err := runtime.EvaluateBreakpoints(h.Ctx, args, cc)
		require.NoError(t, err, "EvaluateBreakpoints iteration %d", i)
	}

	var hits []persistence.BreakpointHitRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 1000, tx)
		hits = r
		return err
	}))
	require.Len(t, hits, 100,
		"drop_oldest must keep the queue bounded at the 100 cap (got %d hits)", len(hits))

	row := getBreakpointRow(t, h, bpID)
	require.NotNil(t, row)
	require.Equal(t, int64(50), row.DroppedCount,
		"dropped_count must equal (writes - cap) = 150-100 = 50; got %d", row.DroppedCount)

	require.True(t, hits[0].Seq > int64(50),
		"earliest kept hit seq=%d should be > 50 (the dropped count) since drop_oldest evicts from the head",
		hits[0].Seq)

	require.LessOrEqual(t, time.Since(testStart), testBudget,
		"test ran longer than budget (%s) — likely a hang in EvaluateBreakpoints / handleOverflow",
		testBudget)
}
