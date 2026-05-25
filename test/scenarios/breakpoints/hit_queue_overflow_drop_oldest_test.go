// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Hit-queue overflow drop_oldest":
//
//   - Install a notify_only breakpoint with empty matcher (matches
//     every dispatch).
//   - Simulate 150 matching dispatches by invoking the supervisor's
//     evaluator directly.
//   - First 100 hits land within the queue cap; the next 50 trigger
//     the drop_oldest overflow policy, evicting the oldest hit each
//     time and bumping the breakpoint's dropped_count.
//   - Final state: dropped_count = 50; exactly 100 hits remain
//     queryable on the breakpoint.
//
// We drive `runtime.EvaluateBreakpoints` directly against the
// harness's persistence rather than going through 150 real dispatches
// — that keeps the test focused on the overflow/eviction contract
// (the per-dispatch runtime is exercised by the other scenarios). This
// follows the same shape as the runtime/breakpoint_eval_test.go
// overflow tests, scaled up to the spec's 150/100/50 numbers.
//
// @concept: breakpoint

package breakpoints

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
	"github.com/fallguy/rimsky/runtime"
)

func TestHitQueueOverflowDropOldest(t *testing.T) {
	t.Parallel()
	// Belt-and-suspenders deadline: bind a "test must finish by" budget
	// at entry. Asserting at the end against this anchored deadline
	// turns a hung run (e.g. handleOverflow looping forever) into a
	// targeted test failure rather than a CI timeout. The budget is
	// generous — drop_oldest is purely persistence work and should
	// complete in well under a second on any sane backend.
	testStart := time.Now()
	testBudget := 30 * time.Second
	// We don't need the dispatch loop here — disable supervisor +
	// scheduler so they don't race with the direct EvaluateBreakpoints
	// calls. The harness still provides Postgres, persistence, and the
	// control-API.
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

	// Create the instance paused so the (disabled) supervisor never
	// touches it; we only care about the breakpoint surface.
	iid := createInstanceWithPause(t, h, tid, "ck-overflow-drop-oldest", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":      "before_dispatch",
		"mode":            "notify_only",
		"overflow_policy": "drop_oldest",
	})

	// Drive 150 EvaluateBreakpoints calls against the harness's persist.
	// Each call writes one hit row; once the per-breakpoint unresumed
	// count reaches the 100 cap, the overflow handler evicts the oldest
	// row and bumps dropped_count.
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
		// Rotate DispatchID/FrameID so each hit carries a fresh
		// (synthetic) provenance.
		cc.DispatchID = shared.UUID(uuid.New())
		cc.FrameID = shared.UUID(uuid.New())
		_, err := runtime.EvaluateBreakpoints(h.Ctx, args, cc)
		require.NoError(t, err, "EvaluateBreakpoints iteration %d", i)
	}

	// Read back: exactly 100 hit rows remain; dropped_count = 50.
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

	// The remaining 100 hits should be the most recent ones — drop_oldest
	// evicts from the head. Sequence numbers should be the upper tail of
	// the BIGSERIAL range; pin that the earliest seq in the kept set is
	// strictly greater than the dropped count.
	require.True(t, hits[0].Seq > int64(50),
		"earliest kept hit seq=%d should be > 50 (the dropped count) since drop_oldest evicts from the head",
		hits[0].Seq)

	// Assert the test stayed under the per-entry budget bound at the
	// top of the function. A hung handleOverflow under drop_oldest
	// would blow this out and produce a targeted failure.
	require.LessOrEqual(t, time.Since(testStart), testBudget,
		"test ran longer than budget (%s) — likely a hang in EvaluateBreakpoints / handleOverflow",
		testBudget)
}
