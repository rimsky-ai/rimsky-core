// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func allLoggedPIDs(t *testing.T, pidLog string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, pids := range readPIDLog(t, pidLog) {
		for pid := range pids {
			out[pid] = true
		}
	}
	return out
}

func terminateAndDeleteInstance(t *testing.T, controlBase, bearerKey, instanceID string) {
	t.Helper()
	termReq, err := http.NewRequest(http.MethodPost, controlBase+"/v1/instances/"+instanceID+"/terminate",
		strings.NewReader("{}"))
	require.NoError(t, err)
	termReq.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		termReq.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	termResp, err := http.DefaultClient.Do(termReq)
	require.NoError(t, err)
	defer termResp.Body.Close()
	require.Equal(t, http.StatusOK, termResp.StatusCode, "POST .../terminate")

	delReq, err := http.NewRequest(http.MethodDelete, controlBase+"/v1/instances/"+instanceID, nil)
	require.NoError(t, err)
	if bearerKey != "" {
		delReq.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	delResp, err := http.DefaultClient.Do(delReq)
	require.NoError(t, err)
	defer delResp.Body.Close()
	require.Equal(t, http.StatusOK, delResp.StatusCode, "DELETE /v1/instances/{id}")
}

// @story: host-agent-control-plane
func TestHostAgent_MainRunScopeCloseReapsSpawnOnRealInstanceTermination(t *testing.T) {
	pidLog := t.TempDir() + "/stub-pid.log"
	t.Setenv("STUBCHILD_PID_LOG", pidLog)

	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-main-scope-reap")
	iid := fx.createLateBindInstance(t, tid, "ck-main-scope-reap", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
	fx.waitForNodeEventKind(t, iid, "terminal/success")

	pid := onlyPID(t, allLoggedPIDs(t, pidLog))
	require.True(t, processAlive(t, pid),
		"the late-bound child spawned for the instance's main run scope must still be alive "+
			"(held, not reaped) immediately after its dispatching node reaches terminal/success")

	terminateAndDeleteInstance(t, fx.h.ControlBase, fx.adminKey, iid.String())

	for processAlive(t, pid) {
		time.Sleep(50 * time.Millisecond)
	}
}
