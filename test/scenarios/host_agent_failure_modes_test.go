// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentNotConnected(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: false})

	tid := fx.deployLateBindTemplate(t, "fail-not-connected")
	iid := fx.createLateBindInstance(t, tid, "ck-not-connected", fx.stubBinary)

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_not_connected", 45*time.Second),
		"expected host_agent_not_connected when no agent is dialed")
}

func TestBindingNotFound(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true, blindProxy: true})

	tid := fx.deployLateBindTemplate(t, "fail-binding-not-found")
	iid := fx.createLateBindInstance(t, tid, "ck-binding-not-found", fx.stubBinary)

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/binding_not_found", 45*time.Second),
		"expected binding_not_found when the proxy cannot find the dispatched binding in its cache")
}

func TestSpawnFailed(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-spawn")
	iid := fx.createLateBindInstance(t, tid, "ck-spawn-failed", "/nonexistent/path/to/binary-xyz")

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed", 45*time.Second),
		"expected spawn_failed when the binding path is not executable")
}

func TestHostAgentDisconnectMidDispatch(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-disconnect")
	fx.cancelAgent()
	select {
	case <-fx.agentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after cancel")
	}
	time.Sleep(300 * time.Millisecond)

	iid := fx.createLateBindInstance(t, tid, "ck-disconnect", fx.stubBinary)

	if fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_disconnected", 30*time.Second) {
		return
	}
	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_not_connected", 20*time.Second),
		"expected a host-agent disconnect class after the agent dropped mid-flight")
}

func TestProxyReconnectAfterAgentRestart(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	fx.cancelAgent()
	select {
	case <-fx.agentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after cancel")
	}
	time.Sleep(300 * time.Millisecond)

	cancel, done := startAgent(t, fx.proxyAddr, fx.adminKey)
	t.Cleanup(func() {
		cancel()
		<-done
	})
	time.Sleep(400 * time.Millisecond)

	tid := fx.deployLateBindTemplate(t, "reconnect-ok")
	iid := fx.createLateBindInstance(t, tid, "ck-reconnect", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
		"run did not complete after agent reconnect")
}
