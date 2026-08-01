// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service-address-book
package scenarios

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestUnresolvedClaimProducerFailsDispatchLoudly(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"vanishing-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "vanished-store", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("vanishing-store", "/region-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-vanished-store", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForDispatchCount(n.ID, 1)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := runtime.RunArgs{
		Persist:               h.Persist,
		Queue:                 h.Queue,
		ClaimHandles:          h.Persist.ClaimHandles(),
		AdvisoryLocker:        h.Driver.AdvisoryLocker(),
		ClaimProducerRegistry: locks.NewRegistry(),
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          "scenario-runner-unresolved-store",
		Pool:                  pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
	}

	_, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err,
		"a store name that resolves nowhere must fail the claimed dispatch through the error path, not error the runner loop")

	var latest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, n.ID, tx)
		latest = r
		return err
	}))
	require.NotNil(t, latest)
	require.Equal(t, cascade.NodeStateFailed, latest.State,
		"an unresolvable store must settle the run failed (loud), never leave it queued (silent)")

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	sawUnresolvedClass := false
	for _, e := range evs.Events {
		if strings.Contains(e.KindRaw, "unresolved_claim_producer") {
			sawUnresolvedClass = true
		}
		for _, v := range e.Payload {
			if s, ok := v.(string); ok && strings.Contains(s, "unresolved_claim_producer") {
				sawUnresolvedClass = true
			}
		}
	}
	require.True(t, sawUnresolvedClass,
		"expected an event naming the unresolved_claim_producer class; got %d events", len(evs.Events))
}
