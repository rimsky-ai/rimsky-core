// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scope-claim scenario coverage — invariant 4b (single-writer-per-
// scope) and invariant 10 (atomic acquisition).
//
// The test starts a stub store-service via the loopback gRPC fixture
// (stores/stub/testfixture.Start), deploys a template whose worker
// node holds an `rw` claim against `selector: "/scope-A"`, and
// confirms that:
//   - the worker node reaches `fresh` (acquisition + dispatch +
//     terminal succeed end-to-end through the wire).
//   - the stub store recorded one `open` call followed by one
//     terminal-side action (commit, by default).
//
// This exercises the §7.3 atomic acquisition path through the gRPC
// bridge — the v3 replacement for the deleted v2 in-process Factory
// pattern. A future variant can introduce a second contending acquirer
// to specifically pin the byte-equal scope-conflict predicate.
package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
	stubfixture "github.com/fallguyconsulting/rimsky/stores/stub/testfixture"
)

// TestScopeClaimEndToEnd drives one scope-claim acquisition
// through the loopback gRPC fixture and asserts the store saw the
// expected verb sequence.
func TestScopeClaimEndToEnd(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
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
				scenario.WithStores(scenario.WriteClaimRef("content", "/scope-A")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-scope-claim", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// The stub store-service must have observed at least one open and
	// one commit/abandon — confirming the wire round-trip happened.
	deadline := time.Now().Add(2 * time.Second)
	var sawOpen, sawTerminal bool
	for time.Now().Before(deadline) {
		for _, c := range sub.Calls() {
			switch c.Verb {
			case "open":
				sawOpen = true
			case "commit", "abandon", "delete", "release":
				sawTerminal = true
			}
		}
		if sawOpen && sawTerminal {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawOpen, "expected stub store to receive Open over the wire")
	require.True(t, sawTerminal, "expected stub store to receive a terminal verb over the wire")
}
