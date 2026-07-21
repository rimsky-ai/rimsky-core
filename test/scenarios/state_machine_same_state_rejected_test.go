// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStateMachineSameStateRejected(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "sm-same", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-sm", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	scopeID := h.GetLatestFrameRootRunScopeID(iid)
	frameID := h.GetRunningFrameID(iid)

	var runID shared.UUID
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		id, err := h.Persist.Nodes().CreateNonCascadeStale(h.Ctx, tx, persistence.NonCascadeStaleInput{
			NodeID:                 n.ID,
			RunScopeID:             scopeID,
			FrameID:                frameID,
			ExecutorName:           "stub",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now(),
			CreationReason:         cascade.CreationReasonOperatorInvalidate,
		})
		runID = id
		return err
	}))

	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		return h.Persist.Nodes().UpdateState(h.Ctx, runID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}))

	err := h.InTx(func(tx persistence.Tx) error {
		return h.Persist.Nodes().UpdateState(h.Ctx, runID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, cascade.ErrIllegalTransition),
		"expected ErrIllegalTransition, got %v", err)
}
