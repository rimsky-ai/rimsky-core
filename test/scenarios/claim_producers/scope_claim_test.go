// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimproducers

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

	livenessOutlivingTestSoReapRecoveryCannotDuplicateVerbs := time.Hour
	h := scenario.Start(t, scenario.HarnessOpts{
		LivenessInterval: livenessOutlivingTestSoReapRecoveryCannotDuplicateVerbs,
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
	require.GreaterOrEqual(t, openCount, 1, "the dispatch must Open its claim over the wire "+
		"(an acquisition transaction that rolls back after Open legally retries with a fresh Open, so the wire count is at-least-once)")
	require.GreaterOrEqual(t, commitCount, 1,
		"the successful write-claim dispatch's sole resolution is Commit "+
			"(terminal-verb delivery is at-least-once by design — the conformance battery requires producers to tolerate retried terminal verbs)")
	var commitClaimIDs []string
	for _, c := range sub.Calls() {
		if c.Verb == "commit" {
			commitClaimIDs = append(commitClaimIDs, c.ClaimID)
		}
	}
	for _, id := range commitClaimIDs {
		require.Equal(t, commitClaimIDs[0], id,
			"every observed Commit must carry the same claim id — the at-least-once wire count is only "+
				"idempotent redelivery of ONE resolution; a second distinct claim id would mean a second "+
				"resolution, not a retry")
	}
	require.Equal(t, 0, abandonCount, "a successful dispatch must not also fire Abandon (double terminal)")
	require.Equal(t, 0, deleteCount, "a successful write-claim dispatch must not fire Delete")
	require.Equal(t, 0, releaseCount, "a successful write-claim dispatch must not fire Release")
}
