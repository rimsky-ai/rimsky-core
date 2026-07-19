// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestParkedFrameHold_InstanceStaysNonTerminalUntilFrameEnds(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resumeAt := time.Now().Add(3 * time.Second)
	h.Stub.WhenType("worker").
		Park(resumeAt)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-frame-hold-terminal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-parked-frame-hold", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)

	require.NoError(t, frame.RunTick(h.Ctx, h.Persist, h.Queue, shared.SilentLogger{}),
		"forcing a frame-settlement sweep while the node is parked must not error")

	runningFrames := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String()+"/frames?state=running")
	require.Len(t, runningFrames["frames"].([]any), 1,
		"a parked run must hold its frame open; the frame-end predicate counts parked as unresolved work")

	instWhileParked := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Nilf(t, instWhileParked["terminated_at"],
		"the instance must not be terminal while its only node is parked; got terminated_at=%v", instWhileParked["terminated_at"])

	h.Stub.WhenType("worker").Success(map[string]any{}, true, "resumed")
	h.WaitForEventKind(worker.ID, "parked_resume_started")
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	for {
		framesAfterResume := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String()+"/frames?state=running")
		if len(framesAfterResume["frames"].([]any)) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	instAfterFrameEnd := getJSONMapInst(t, h.ControlBase+"/v1/instances/"+iid.String())
	require.Nilf(t, instAfterFrameEnd["terminated_at"],
		"an instance is durable by default and must not self-terminate merely because its frame ended; "+
			"got terminated_at=%v", instAfterFrameEnd["terminated_at"])

	termResp := doPost(t, h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, termResp.status,
		"terminate must return 200: %s", string(termResp.raw))
	waitForInstanceTerminated(t, h, iid)
}
