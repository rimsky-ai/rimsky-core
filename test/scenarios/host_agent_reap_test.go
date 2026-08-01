// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentReapOnRunScopeTerminal(t *testing.T) {
	termLog := t.TempDir() + "/stub-term.log"
	t.Setenv("STUBCHILD_TERM_LOG", termLog)

	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-reap")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-reap", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	termReq, err := http.NewRequest(http.MethodPost, fx.h.ControlBase+"/v1/instances/"+iid.String()+"/terminate", nil)
	require.NoError(t, err)
	termReq.Header.Set("Authorization", "Bearer "+fx.adminKey)
	termResp, err := http.DefaultClient.Do(termReq)
	require.NoError(t, err)
	_ = termResp.Body.Close()
	require.Less(t, termResp.StatusCode, 300, "POST /terminate should succeed, got %d", termResp.StatusCode)

	req, err := http.NewRequest(http.MethodDelete, fx.h.ControlBase+"/v1/instances/"+iid.String(), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+fx.adminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "DELETE /instances should succeed, got %d", resp.StatusCode)

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(termLog)
		return statErr == nil
	}, 30*time.Second, 100*time.Millisecond,
		"spawned child was not reaped (OnRunScopeTerminal reap never reached the agent)")
}
