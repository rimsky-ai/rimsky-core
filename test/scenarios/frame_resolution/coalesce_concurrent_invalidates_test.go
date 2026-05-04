// Verifies the §7.3 step 1 coalesce upsert is race-free under concurrent
// producers. Two operator invalidates landing simultaneously must both
// succeed (one INSERTs, the other DO UPDATEs source_node_ids) — neither
// can hit a unique-violation that bubbles up as a 5xx.
//
// Without the atomic ON CONFLICT form (UPDATE-then-INSERT split), two
// goroutines can both find no queued row in the UPDATE step and both
// attempt INSERT; the loser sees pgconn.PgError SQLSTATE 23505 and
// returns an error. This test fires N concurrent invalidates from
// goroutines and asserts no error surfaces, then asserts the final
// queued-row count obeys uq_rimsky_frames_coalesce_queued (≤ 1).
package frame_resolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/frame"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
)

func TestCoalesceConcurrentInvalidatesNoUniqueViolation(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Slow stub so the first frame is genuinely in flight while we fire
	// the concurrent batch — we want the partial unique index actually
	// guarding a queued row, not a freshly-completing race.
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok").Delay(3 * time.Second)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "coalesce-concurrent", Version: "1",
		FrameResolution: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-coalesce-concurrent", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait until the first frame is running so subsequent invalidates
	// must coalesce rather than become the running frame.
	require.True(t, waitForFramesByState(t, h, iid, "running", 1, 5*time.Second),
		"first frame did not enter running")

	// Fire N concurrent invalidates. Each runs in its own short tx via
	// the persistence driver, exactly mirroring how two operator
	// invalidate handlers would race in production.
	const N = 32
	var wg sync.WaitGroup
	errs := make(chan error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			err := h.Driver.Store().Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
				_, err := frame.EnqueueOrCoalesce(ctx, h.Driver.Store(), tx,
					uuid.UUID(iid), uuid.UUID(worker.ID))
				return err
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err,
			"concurrent EnqueueOrCoalesce(coalesce) must not surface 5xx-class errors (e.g. unique-violation)")
	}

	// uq_rimsky_frames_coalesce_queued: at most one queued coalesce row
	// at any moment. Sample for a short window in case the running frame
	// is just about to terminate.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		n := countFramesByState(t, h, iid, "queued")
		require.LessOrEqual(t, n, 1,
			"observed %d queued coalesce rows; uq_rimsky_frames_coalesce_queued violated", n)
		time.Sleep(20 * time.Millisecond)
	}

	// Drain to terminal: exactly 2 completed frames (initial running +
	// the single coalesced trailing frame).
	require.Eventually(t, func() bool {
		return countFramesByState(t, h, iid, "completed") == 2
	}, 30*time.Second, 100*time.Millisecond,
		"expected exactly 2 completed frames after concurrent coalesce drain")
}
