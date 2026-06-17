// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-opaque-executor-scratch proof — verifies the round-trip
// property of executor-attached opaque scratch bytes across the
// three prior-dispatch recovery dispositions (stale_recovery,
// retry_after_error, recalculate).
//
// The retry_after_error variant drives the round-trip end to end:
// the in-process handler writes scratch via the unary Outcome's
// Success/Error `scratch` field on its first dispatch; the
// supervisor's error-policy retry chain copies the scratch onto the
// successor dispatch row; the handler's second invocation captures
// the incoming `req.scratch` field and the test asserts byte-for-
// byte equality.
//
// The stale_recovery and recalculate variants drive the persistence-
// layer copy directly via `Queue.EnqueueInTx`-with-prior-dispatch
// (the same shape the runtime's recovery sites use). This shape is
// what `concept:opaque-executor-scratch` requires: regardless of
// which disposition drove the successor enqueue, the persistence
// layer copies the bytes verbatim onto the new row. Driving an
// actual second supervisor dispatch through these variants would
// duplicate the retry_after_error end-to-end pin without adding
// coverage at the persistence-vs-wire boundary that already holds
// it. The persistence conformance suite
// (`code:lib/foundation/persistence/conformance/recovery_aware_dispatch.go`)
// holds the same property at the persistence layer.
//
// @story: opaque-executor-scratch
// @concept: executor
package scenarios

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @deliberate: randomScratch returns a scratch envelope: a fixed
// signature followed by a unique random suffix. Each test variant
// gets its own bytes so a fixture leak between variants would
// surface as a content mismatch.
func randomScratch(t *testing.T, prefix string) []byte {
	t.Helper()
	suffix := make([]byte, 16)
	_, err := rand.Read(suffix)
	require.NoError(t, err, "crypto/rand.Read")
	out := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	out = append(out, []byte(prefix)...)
	out = append(out, suffix...)
	return out
}

// scratchHandler is an inproc handler whose first dispatch writes
// scratch on the terminal outcome (Error.scratch when failOnFirst,
// Success.scratch otherwise) and whose subsequent dispatches record
// the inbound `req.scratch` for assertion.
type scratchHandler struct {
	writeBytes  []byte
	failOnFirst bool
	dispatches  int64
	mu          sync.Mutex
	seenOnRetry []byte
}

func (h *scratchHandler) Execute(
	_ context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext,
) (*genv1.Outcome, error) {
	n := atomic.AddInt64(&h.dispatches, 1)
	if n == 1 {
		if h.failOnFirst {
			return &genv1.Outcome{
				Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
					ErrorClass: "executor_runtime_error",
					Scratch:    h.writeBytes,
				}},
			}, nil
		}
		return &genv1.Outcome{
			Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
				Changed:       true,
				ChangeSummary: "first",
				Scratch:       h.writeBytes,
			}},
		}, nil
	}
	h.mu.Lock()
	h.seenOnRetry = append([]byte(nil), req.Scratch...)
	h.mu.Unlock()
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			Changed:       true,
			ChangeSummary: "recovery",
		}},
	}, nil
}

func (h *scratchHandler) snapshot() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.seenOnRetry...)
}

// TestScratchRoundTripE2E_RetryAfterError drives the
// retry_after_error path end to end: handler's first dispatch
// returns Error with scratch attached; supervisor's error-policy
// retry chain copies the scratch onto the successor row;
// second invocation captures `req.scratch` and asserts byte-for-
// byte equality.
//
// @concept: error-policy
func TestScratchRoundTripE2E_RetryAfterError(t *testing.T) {
	t.Parallel()
	const url = "inproc://scratch-round-trip-retry"
	scratchBytes := randomScratch(t, "retry_after_error")
	h := &scratchHandler{writeBytes: scratchBytes, failOnFirst: true}
	harness := scenario.Start(t, scenario.HarnessOpts{
		ExtraInprocHandlers: map[string]executor.InProcessHandler{url: h},
		ExtraExecutors: map[string]executor.Endpoint{
			url: {Transport: "inproc", URL: url},
		},
		RefValidationMode: node.RefValidateAvailable,
	})

	tid := harness.DeployTemplate(node.TemplateSpec{
		Name: "scratch-retry-after-error", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: url,
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"executor_runtime_error": {
						Policy: []node.PolicyAction{
							{Action: "retry", Count: 1, BaseDelayMs: 50, Backoff: "linear"},
						},
					},
				},
			}),
		},
	})
	iid := harness.CreateInstance(tid, "ck-scratch-retry", map[string]any{})
	n := harness.FindNode(iid, "worker")
	require.NotNil(t, n, "worker node missing")

	// @deliberate: Wait for the retry-success terminal — the second
	// dispatch transitions the node to fresh.
	require.True(t,
		harness.WaitForNodeState(n.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker MUST reach fresh after retry — first dispatch errors with retry policy, second succeeds")

	require.Equal(t, int64(2), atomic.LoadInt64(&h.dispatches),
		"handler MUST be dispatched exactly twice (first errors, second succeeds via retry)")
	require.True(t, bytes.Equal(h.snapshot(), scratchBytes),
		"retry-after-error recovery dispatch MUST receive the prior row's scratch verbatim: want=%x got=%x",
		scratchBytes, h.snapshot())
}

// TestScratchRoundTripE2E_StaleRecovery + _Recalculate drive the
// persistence-layer carry-forward for the other two dispositions.
// These variants:
//
//  1. Run a first dispatch through the in-process handler with
//     scratch attached to its Success terminal. The supervisor
//     persists the scratch on that dispatch's row.
//  2. Identify the now-terminal dispatch row's id.
//  3. Call `Queue.LoadScratchInTx` + `Queue.EnqueueInTx` directly
//     with the appropriate disposition, mirroring the runtime's
//     recovery enqueue sites.
//  4. Read the new dispatch row's scratch_inline column and assert
//     verbatim equality with the original bytes.
//
// The retry_after_error variant above pins the executor-side
// `req.scratch` round trip end to end; these two variants pin the
// persistence-layer copy across the other dispositions, which is
// the wire-to-row boundary the story acceptance keys on.
//
// @concept: opaque-executor-scratch

func TestScratchRoundTripE2E_StaleRecovery(t *testing.T) {
	t.Parallel()
	runDispositionVariant(t, "stale_recovery")
}

func TestScratchRoundTripE2E_Recalculate(t *testing.T) {
	t.Parallel()
	runDispositionVariant(t, "recalculate")
}

func runDispositionVariant(t *testing.T, disposition string) {
	t.Helper()
	url := fmt.Sprintf("inproc://scratch-round-trip-%s", disposition)
	scratchBytes := randomScratch(t, disposition)
	h := &scratchHandler{writeBytes: scratchBytes, failOnFirst: false}
	harness := scenario.Start(t, scenario.HarnessOpts{
		ExtraInprocHandlers: map[string]executor.InProcessHandler{url: h},
		ExtraExecutors: map[string]executor.Endpoint{
			url: {Transport: "inproc", URL: url},
		},
		RefValidationMode: node.RefValidateAvailable,
	})

	tid := harness.DeployTemplate(node.TemplateSpec{
		Name: "scratch-" + disposition, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: url}),
		},
	})
	iid := harness.CreateInstance(tid, "ck-scratch-"+disposition, map[string]any{})
	n := harness.FindNode(iid, "worker")
	require.NotNil(t, n, "worker node missing")
	require.True(t,
		harness.WaitForNodeState(n.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker MUST reach fresh after first dispatch — initial scratch attach failed?")

	// @deliberate: Find the now-terminal dispatch row carrying the
	// scratch. Use the terminal-phase predicate
	// (`phase IN ('completed','failed')`) to locate it; the row
	// survives past active terminal so frame-end + retention + run-
	// tree aggregation can read its terminal state.
	var dispatchIDText, frameIDText, runScopeIDText string
	harness.QueryRowSQL(`
		SELECT id::text, frame_id::text, run_scope_id::text
		  FROM rimsky_node_runs
		 WHERE node_id = $1 AND phase = 'completed'
		 ORDER BY enqueued_at DESC
		 LIMIT 1`,
		[]any{n.ID}, &dispatchIDText, &frameIDText, &runScopeIDText)
	require.NotEmpty(t, dispatchIDText, "terminal dispatch row not found")

	priorUUID, err := uuid.Parse(dispatchIDText)
	require.NoError(t, err)
	frameUUID, err := uuid.Parse(frameIDText)
	require.NoError(t, err)
	runScopeUUIDParsed, err := uuid.Parse(runScopeIDText)
	require.NoError(t, err)
	priorID := shared.UUID(priorUUID)
	frameID := shared.UUID(frameUUID)
	runScopeID := shared.UUID(runScopeUUIDParsed)

	// @deliberate: Drive the recovery enqueue directly. This mirrors
	// the runtime's recovery sites:
	//   stale_recovery → conductor.go's quiet-period sweep would
	//     eventually mark the row stale; here we drive the same
	//     EnqueueInTx shape directly to pin the persistence-layer
	//     copy.
	//   recalculate → cascade_recalculate.go calls
	//     LoadScratchInTx + EnqueueInTx with this disposition; same
	//     shape exercised here.
	require.NoError(t, harness.Persist.Transaction(harness.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		inline, handle, backend, lerr := harness.Queue.LoadScratchInTx(ctx, tx, priorID)
		if lerr != nil {
			return lerr
		}
		return harness.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      n.ID,
			ExecutorName:                url,
			RequiredStores:              []string{},
			EnqueuedAt:                  time.Now().Add(-time.Second),
			FrameID:                     frameID,
			RunScopeID:                  runScopeID,
			PriorDispatchID:             &priorID,
			PriorDispatchDisposition:    disposition,
			InitialScratchInline:        inline,
			InitialScratchHandle:        handle,
			InitialScratchHandleBackend: backend,
		}, tx)
	}), "recovery EnqueueInTx")

	// @constraint: The successor dispatch row's scratch_inline MUST
	// equal the prior row's bytes verbatim — the persistence layer
	// carries them across without inspection (`concept:inertness`).
	var gotInline []byte
	harness.QueryRowSQL(`
		SELECT scratch_inline
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		   AND prior_dispatch_id = $2
		   AND prior_dispatch_disposition = $3`,
		[]any{n.ID, priorID, disposition}, &gotInline)
	require.True(t, bytes.Equal(gotInline, scratchBytes),
		"%s recovery row MUST carry prior scratch verbatim: want=%x got=%x",
		disposition, scratchBytes, gotInline)
}
