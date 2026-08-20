// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

func TestHostAgentNotConnected(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: false})

	tid := fx.deployLateBindTemplate(t, "fail-not-connected")
	iid := fx.createLateBindInstance(t, tid, "ck-not-connected", fx.stubBinary)

	fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_not_connected")
}

func TestBindingNotFound(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true, blindProxy: true})

	tid := fx.deployLateBindTemplate(t, "fail-binding-not-found")
	iid := fx.createLateBindInstance(t, tid, "ck-binding-not-found", fx.stubBinary)

	fx.waitForNodeEventKind(t, iid, "terminal/error/binding_not_found")
}

func TestSpawnFailed(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-spawn")
	iid := fx.createLateBindInstance(t, tid, "ck-spawn-failed", "/nonexistent/path/to/binary-xyz")

	fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed")
}

func TestHostAgentDisconnectMidDispatch(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-disconnect")
	bindings := map[string]any{
		lateBindServiceName: map[string]any{
			"path":            fx.stubBinary,
			"env":             map[string]string{"STUBCHILD_NO_BIND": "1"},
			"timeout_seconds": 20,
		},
	}
	iid := fx.h.CreateInstanceWithServiceBindings(tid, "ck-disconnect", fx.adminKey, map[string]any{}, bindings)

	waitForProcessRunning(t, fx.stubBinary)

	fx.cancelAgent()
	<-fx.agentDone

	fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_disconnected")
}

func waitForProcessRunning(t *testing.T, binaryPath string) {
	t.Helper()
	awaited.Until(t, "a running process matching "+binaryPath, func() bool {
		out, err := exec.Command("pgrep", "-f", binaryPath).Output()
		return err == nil && len(strings.TrimSpace(string(out))) > 0
	})
}

func TestProxyReconnectAfterAgentRestart(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	fx.cancelAgent()
	<-fx.agentDone

	cancel, done, statusFile := startAgent(t, fx.proxyAddr, agentStartOptions{APIKey: fx.adminKey})
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitAgentConnected(t, statusFile)

	tid := fx.deployLateBindTemplate(t, "reconnect-ok")
	iid := fx.createLateBindInstance(t, tid, "ck-reconnect", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
}
