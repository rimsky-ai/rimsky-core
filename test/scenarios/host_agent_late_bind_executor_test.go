// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_late_bind_executor_test.go — end-to-end happy path for the
// host-agent + host-agent-proxy late-bound executor pattern. A template
// declares late_bind_services: [codegen] with a node executor: codegen; an
// instance binds codegen to the stubchild binary; the supervisor resolves
// codegen through the proxy, which finds the connected agent, spawns the
// stub, and tunnels Execute through to it. The run reaches terminal/success.
//
// Verifies the real dispatch path: supervisor → proxy (Executor.Execute with
// x-rimsky-service-name) → agent → spawned stub binary → StreamClose.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentLateBindExecutorHappyPath(t *testing.T) {
	// @deliberate: Not parallel: execs real child processes and binds free ports; keep it
	// serial so the port reservations and process reaping stay predictable.
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-happy")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-happy", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	// @constraint: The dispatch must traverse proxy → agent → stub and the run must reach
	// fresh (terminal/success), proving the tunnel carried the Execute and
	// the spawned binary handled it.
	require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
		"late-bound worker did not reach fresh via proxy+agent dispatch")

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/success", 10*time.Second),
		"expected terminal/success event from the proxy-tunneled stub executor")
}
