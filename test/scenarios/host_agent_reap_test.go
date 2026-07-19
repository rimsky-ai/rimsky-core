// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
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

	require.NoError(t, fx.h.InTx(func(tx persistence.Tx) error {
		return fx.h.Persist.Instances().MarkTerminated(fx.h.Ctx, iid, tx)
	}))

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
