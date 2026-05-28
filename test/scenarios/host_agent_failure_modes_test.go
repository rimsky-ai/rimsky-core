// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_failure_modes_test.go — end-to-end failure modes for the
// host-agent + host-agent-proxy late-bound dispatch path. Each proxy
// resolution/spawn failure surfaces as an executor StreamClose{Error,
// error_class}; the runtime emits a terminal/error/<class> signal event we
// assert on. Reconnect recovery is covered by restarting the in-process
// agent and confirming a fresh run completes.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

// TestHostAgentNotConnected: an instance with bindings but no agent dialed
// resolves to host_agent_not_connected.
func TestHostAgentNotConnected(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: false})

	tid := fx.deployLateBindTemplate(t, "fail-not-connected")
	iid := fx.createLateBindInstance(t, tid, "ck-not-connected", fx.stubBinary)

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_not_connected", 45*time.Second),
		"expected host_agent_not_connected when no agent is dialed")
}

// TestBindingNotFound: the resolver routes the dispatch to the proxy (the
// instance row carries the codegen binding), but the proxy's cache is empty
// (blind proxy: no lifecycle subscription, no GET fallback), so its binding
// lookup misses and it surfaces binding_not_found. This exercises the
// proxy's binding guard under cache-staleness rather than the resolver's own
// unresolved path (which fires when the binding is absent from the row too).
func TestBindingNotFound(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true, blindProxy: true})

	tid := fx.deployLateBindTemplate(t, "fail-binding-not-found")
	iid := fx.createLateBindInstance(t, tid, "ck-binding-not-found", fx.stubBinary)

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/binding_not_found", 45*time.Second),
		"expected binding_not_found when the proxy cannot find the dispatched binding in its cache")
}

// TestSpawnFailed: the binding path points at a non-existent binary, so the
// agent's exec() fails and the proxy surfaces spawn_failed.
func TestSpawnFailed(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-spawn")
	iid := fx.createLateBindInstance(t, tid, "ck-spawn-failed", "/nonexistent/path/to/binary-xyz")

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed", 45*time.Second),
		"expected spawn_failed when the binding path is not executable")
}

// TestHostAgentDisconnectMidDispatch: drop the agent right as a dispatch is
// in flight. With the agent's stream gone, the dispatch surfaces a
// host-agent disconnect class (host_agent_not_connected once the proxy has
// dropped the agent, or host_agent_disconnected if the stream tears down
// mid-spawn). We accept either disconnect-family class.
func TestHostAgentDisconnectMidDispatch(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "fail-disconnect")
	// Drop the agent before dispatch so the proxy has no live connection.
	fx.cancelAgent()
	select {
	case <-fx.agentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after cancel")
	}
	// Give the proxy a moment to observe the dropped stream.
	time.Sleep(300 * time.Millisecond)

	iid := fx.createLateBindInstance(t, tid, "ck-disconnect", fx.stubBinary)

	if fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_disconnected", 30*time.Second) {
		return
	}
	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/host_agent_not_connected", 20*time.Second),
		"expected a host-agent disconnect class after the agent dropped mid-flight")
}

// TestProxyReconnectAfterAgentRestart: after the agent drops and a fresh
// agent connects under the same owner key, a subsequent run completes
// successfully — proving the proxy accepts reconnects and routes to the new
// connection.
func TestProxyReconnectAfterAgentRestart(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	// Drop the original agent.
	fx.cancelAgent()
	select {
	case <-fx.agentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after cancel")
	}
	time.Sleep(300 * time.Millisecond)

	// Reconnect a fresh agent under the same owner key.
	cancel, done := startAgent(t, fx.proxyAddr, fx.ownerKeyID)
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
