// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	})

	tid := harness.DeployTemplate(node.TemplateSpec{
		Name: "scratch-retry-after-error", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "worker",
				Executor:     url,
				MaxRetries:   node.IntPtr(1),
				RetryBackoff: &node.RetryBackoffConfig{BaseDelayMs: 50, Kind: "linear"},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"executor_runtime_error": {Action: "retry"},
				},
			}),
		},
	})
	iid := harness.CreateInstance(tid, "ck-scratch-retry", map[string]any{})
	n := harness.FindNode(iid, "worker")
	require.NotNil(t, n, "worker node missing")

	harness.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	require.Equal(t, int64(2), atomic.LoadInt64(&h.dispatches),
		"handler MUST be dispatched exactly twice (first errors, second succeeds via retry)")
	require.True(t, bytes.Equal(h.snapshot(), scratchBytes),
		"retry-after-error recovery dispatch MUST receive the prior row's scratch verbatim: want=%x got=%x",
		scratchBytes, h.snapshot())
}

// @story: opaque-executor-scratch

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
	harness.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	var dispatchIDText, frameIDText, runScopeIDText string
	harness.QueryRowSQL(`
		SELECT id::text, frame_id::text, run_scope_id::text
		  FROM rimsky_node_runs
		 WHERE node_id = $1 AND state = 'fresh'
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
	priorRunPtr := shared.UUID(priorUUID)
	frameID := shared.UUID(frameUUID)
	runScopeID := shared.UUID(runScopeUUIDParsed)

	require.NoError(t, harness.Persist.Transaction(harness.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		inline, handle, backend, lerr := harness.Queue.LoadScratch(ctx, priorRunPtr, tx)
		if lerr != nil {
			return lerr
		}
		return harness.Queue.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                      n.ID,
			ExecutorName:                url,
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  time.Now().Add(-time.Second),
			FrameID:                     frameID,
			RunScopeID:                  runScopeID,
			PriorNodeRunID:              &priorRunPtr,
			PriorDispatchDisposition:    disposition,
			InitialScratchInline:        inline,
			InitialScratchHandle:        handle,
			InitialScratchHandleBackend: backend,
		}, tx)
	}), "recovery Enqueue")

	var gotInline []byte
	harness.QueryRowSQL(`
		SELECT scratch_inline
		  FROM rimsky_node_runs
		 WHERE node_id = $1
		   AND prior_dispatch_id = $2
		   AND prior_dispatch_disposition = $3`,
		[]any{n.ID, priorRunPtr, disposition}, &gotInline)
	require.True(t, bytes.Equal(gotInline, scratchBytes),
		"%s recovery row MUST carry prior scratch verbatim: want=%x got=%x",
		disposition, scratchBytes, gotInline)

	for atomic.LoadInt64(&h.dispatches) < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, bytes.Equal(h.snapshot(), scratchBytes),
		"%s recovery dispatch MUST deliver the prior scratch to the real executor handler, not just the row: want=%x got=%x",
		disposition, scratchBytes, h.snapshot())
}
