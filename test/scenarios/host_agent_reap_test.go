// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_reap_test.go — end-to-end reap path for the host-agent +
// host-agent-proxy stack. A late-bound executor spawns the stubchild via the
// proxy; deleting the instance closes its main run-scope and fires
// OnRunScopeTerminal to the proxy (the registered lifecycle peer). The proxy
// looks up the spawn keyed to the run-scope and sends a Reap to the agent,
// which SIGTERMs the child. Under per-run-scope spawn isolation the proxy
// keys spawns by run_scope_id (the supervisor stamps ExecuteRequest.run_scope_id
// = the run-tree row's RunScopeID; for this non-fanned-out worker that is the
// instance's main run-scope id), and the DELETE fires OnRunScopeTerminal for
// that same main run-scope id — so the run-scope-keyed reap matches end to end.
// The test fires the real DELETE so production's run-scope-id ≠ instance-id is
// exercised.
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
	// @deliberate: Not parallel: execs real child processes and binds free ports.
	termLog := t.TempDir() + "/stub-term.log"
	t.Setenv("STUBCHILD_TERM_LOG", termLog)

	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-reap")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-reap", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	// @deliberate: Drive the run so the proxy lazily spawns the stub child for this
	// instance's binding.
	require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
		"late-bound worker did not reach fresh via proxy+agent dispatch")

	// @deliberate: Mark the instance terminal so DELETE is sanctioned (spec §2.4 only
	// permits deleting a terminal instance). The DELETE then synchronously
	// closes the main run-scope and fires OnRunScopeTerminal — the reap path
	// under test.
	require.NoError(t, fx.h.InTx(func(tx persistence.Tx) error {
		return fx.h.Persist.Instances().MarkTerminated(fx.h.Ctx, iid, tx)
	}))

	// @deliberate: Delete the instance: control-api closes the main run-scope and fires
	// OnRunScopeTerminal{run_scope_id (≠ instance id), instance_id} to the
	// proxy, which reaps the spawn keyed to the instance.
	req, err := http.NewRequest(http.MethodDelete, fx.h.ControlBase+"/v1/instances/"+iid.String(), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+fx.adminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "DELETE /instances should succeed, got %d", resp.StatusCode)

	// @constraint: The reap must reach the agent, which SIGTERMs the child; the child
	// touches STUBCHILD_TERM_LOG on the signal.
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(termLog)
		return statErr == nil
	}, 30*time.Second, 100*time.Millisecond,
		"spawned child was not reaped (OnRunScopeTerminal reap never reached the agent)")
}
