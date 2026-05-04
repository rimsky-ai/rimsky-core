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

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/locks/storetest"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
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
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "scenario")

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

	fake := storetest.NewFake("content", locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}})
	reg := locks.NewRegistry()
	reg.Add("content", fake)

	args := integration.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		LockHolders:       h.Persist.LockHolders(),
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
	out, err := integration.RunNode(h.Ctx, args, nil)
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
