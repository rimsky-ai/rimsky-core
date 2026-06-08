// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_anonymous_mode_latebind_test.go — end-to-end proof that
// anonymous-mode and host-agent late-binding are NOT mutually exclusive
// (story S-hostagent-anonymous-mode-latebind). An operator running an
// unauthenticated bootstrap (anonymous-mode) deployment must be able to
// register and dispatch to late-bound services from an instance created in
// anonymous mode.
//
// Real stack: control-api (in anonymous mode — zero api-keys minted) +
// supervisor + rimsky-host-agent-proxy + in-process host-agent +
// a real exec'd stubchild as the late-bound executor binary. The template is
// registered/deployed and the instance is created via the anonymous path
// (no bearer), so the instance row's created_by_api_key_id is empty. The
// agent connects under the anonymous routing identity.
//
// RED (current tree): an anonymous instance persists with an empty owner
// api-key (control-api threads CreatedByAPIKeyID = ident.KeyID, which is nil
// for the synthetic anonymous identity). The proxy short-circuits when the
// cache entry's ownerAPIKeyID == "" and returns host_agent_not_connected
// ("instance has no owner api-key (anonymous-mode)") BEFORE any agent lookup
// — so the dispatch never reaches the connected agent and the worker never
// settles fresh. This test asserts the OBSERVABLE success outcome (worker
// reaches cascade.NodeStateFresh and emits terminal/success), so it FAILS
// until a later GREEN pass teaches the proxy to resolve an anonymous-owner
// instance to an agent registered under the anonymous routing identity rather
// than terminating with host_agent_not_connected.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentAnonymousModeLateBind(t *testing.T) {
	// Not parallel: execs real child processes and binds free ports; keep it
	// serial so the port reservations and process reaping stay predictable.
	// anonymous: true skips minting an owner key (which would flip the
	// deployment out of anonymous mode) and registers the agent under the
	// anonymous routing identity.
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true, anonymous: true})

	// fx.adminKey is empty in anonymous mode, so deployLateBindTemplate /
	// createLateBindInstance both POST with no bearer — the anonymous path.
	// The resulting instance row carries an empty created_by_api_key_id.
	tid := fx.deployLateBindTemplate(t, "anon-late-bind")
	iid := fx.createLateBindInstance(t, tid, "ck-anon-late-bind", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	// The dispatch must traverse proxy → agent → stub and the run must reach
	// fresh (terminal/success), proving the proxy resolved the anonymous-owner
	// instance to the connected agent rather than terminating with
	// host_agent_not_connected on the empty-owner short-circuit.
	require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
		"anonymous-mode late-bound worker did not reach fresh — the proxy must "+
			"resolve an empty-owner (anonymous) instance to the agent under the "+
			"anonymous routing identity instead of host_agent_not_connected")

	require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/success", 10*time.Second),
		"expected terminal/success event from the proxy-tunneled stub executor "+
			"on an anonymous-mode instance")
}
