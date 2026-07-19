// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestScopeClaimEndToEnd(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "scope-claim", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.WriteClaimRef("content", "/scope-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-scope-claim", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	var openCount, commitCount, abandonCount, deleteCount, releaseCount int
	for commitCount == 0 {
		openCount, commitCount, abandonCount, deleteCount, releaseCount = 0, 0, 0, 0, 0
		for _, c := range sub.Calls() {
			switch c.Verb {
			case "open":
				openCount++
			case "commit":
				commitCount++
			case "abandon":
				abandonCount++
			case "delete":
				deleteCount++
			case "release":
				releaseCount++
			}
		}
		if commitCount == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	require.Equal(t, 1, openCount, "expected exactly one Open over the wire")
	require.Equal(t, 1, commitCount,
		"expected exactly one Commit terminal verb — the successful write-claim dispatch's sole resolution")
	require.Equal(t, 0, abandonCount, "a successful dispatch must not also fire Abandon (double terminal)")
	require.Equal(t, 0, deleteCount, "a successful write-claim dispatch must not fire Delete")
	require.Equal(t, 0, releaseCount, "a successful write-claim dispatch must not fire Release")
}
