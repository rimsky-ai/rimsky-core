// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scope-envelope coverage — verifies the `template_id` and
// `instance_id` fields populated on the supervisor-side OpenRequest
// reach the store at dispatch time. Pin per spec §13.4 (the wire
// envelope) and §4.2 (rimsky-inert envelope).
//
// Uses the in-process storetest.Fake so the recorded FakeCall
// observably carries TemplateID / InstanceID — the gRPC bridge
// passes them through but the existing stub Store discards them
// in its `Open` shim. The relevant rimsky-side property — that
// the supervisor populates the envelope before dispatching — is
// already exercised at this seam.
package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestOpenScopeEnvelopeReachesStore deploys a one-node template,
// creates an instance, drives the worker through dispatch with a
// runner-local Fake registry, and asserts the recorded Open call
// carries non-empty TemplateID (the canonical spec hash) and
// non-empty InstanceID (the rimsky-generated UUID).
func TestOpenScopeEnvelopeReachesStore(t *testing.T) {
	t.Parallel()

	// Loopback stub for control-api template validation. The runner
	// below substitutes its own in-process Fake registry so we can
	// observe TemplateID/InstanceID directly on the recorded call.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "scope-envelope", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("content", "/region-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-scope-envelope", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForDispatch(n.ID, 5*time.Second))

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	fake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg := locks.NewRegistry()
	reg.Add("content", fake)

	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     reg,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner-scope",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"content"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}
	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner should have dispatched the node")

	// Find the Open call on the Fake. The supervisor must have
	// populated the envelope from the dispatch row's instance →
	// template lookup before issuing Open.
	calls := fake.Calls()
	var openCall *storetest.FakeCall
	for i := range calls {
		if calls[i].Verb == "open" {
			openCall = &calls[i]
			break
		}
	}
	require.NotNil(t, openCall, "fake should have observed an Open call")
	require.Equal(t, tid, openCall.TemplateID,
		"OpenRequest.template_id must equal the deploying template's content hash")
	require.Equal(t, iid.String(), openCall.InstanceID,
		"OpenRequest.instance_id must equal the rimsky-generated instance UUID")
}
