// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cross-queue concurrency through the loopback gRPC fixture.
// Two pick policies share the same sub-root; both auto-discover
// folder "alpha". Both acquirer nodes produce byte-equal regions
// (json("docs/alpha")), so rimsky's conflict predicate serializes
// them. Eventually both reach `fresh` in some order — the losing
// acquirer recycles via on_give_up: recycle.
package stores

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	"github.com/fallguyconsulting/rimsky/stores/common/action"
	fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"
	fsfixture "github.com/fallguyconsulting/rimsky/stores/filesystem/testfixture"
)

func TestFsCrossQueueConcurrency(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))

	p1 := &fsstore.PickPolicy{
		Root: "docs", OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
	}
	p2 := &fsstore.PickPolicy{
		Root: "docs", OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
	}
	grpcEndpoint, _, teardown := fsfixture.Start(t, fsfixture.Config{
		Root:         root,
		PickPolicies: map[string]*fsstore.PickPolicy{"@r1": p1, "@r2": p2},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"docs": {
					Endpoint:     "grpc://" + grpcEndpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	// The stub executor's WhenType map is exact-match — one entry
	// per distinct node Type, otherwise the executor errors out.
	h.Stub.WhenType("worker-r1").Success(map[string]any{}, true, "scenario")
	h.Stub.WhenType("worker-r2").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-cross-queue", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker-r1", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("docs", "@r1")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker-r2", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("docs", "@r2")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-xqueue", map[string]any{})

	n1 := h.FindNode(iid, "worker-r1")
	n2 := h.FindNode(iid, "worker-r2")
	require.NotNil(t, n1)
	require.NotNil(t, n2)

	// Both nodes must eventually reach fresh — the conflict only
	// delays the loser, doesn't break it. 30s is generous for
	// visibility-timeout / scheduler-tick combinations.
	require.True(t, h.WaitForNodeState(n1.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker-r1 did not reach fresh")
	require.True(t, h.WaitForNodeState(n2.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker-r2 did not reach fresh")
}
