// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cross-queue concurrency through a real rimsky + fs-store + stub-
// executor stack. Two pick policies share the same sub-root; both
// auto-discover folder "alpha". Both worker nodes hold pick-policy
// claims against byte-equal regions (`{"folder":"docs/alpha"}`), so
// rimsky's conflict predicate serializes them. Both nodes must
// eventually reach terminal state — the losing acquirer recycles via
// `on_give_up: recycle`.
//
// The pre-2026-05-24-repo-reorganization version drove rimsky in-
// process via `graph/scenario.Start`. Post-reorganization rimsky is
// reached only over its public HTTP API. The rewrite uses
// `test/harness.BringUpRimsky` to bring up rimsky/all + the locally-
// built filesystem-store image + the in-tree executor-stub image as
// peer containers on the shared docker network.
package stores

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestFSCrossQueueConcurrency drives the cross-queue conflict scenario
// end-to-end against the live rimsky stack. Both nodes target the same
// auto-discovered folder; rimsky's conflict predicate serializes them
// and both must eventually reach a terminal node-state.
func TestFSCrossQueueConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: shared network must exist before peers start — the
	// fs-store and executor-stub peers must be reachable when rimsky/all
	// starts because rimsky's control-api fires a Capabilities handshake
	// at startup.
	netName := harness.NewNetwork(ctx, t)

	fs := harness.StartFilesystemStore(ctx, t, netName, "store-filesystem",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@r1": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
				"@r2": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}},
		})
	_ = fs

	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "fs-cross-queue",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker-r1",
					"executor": "stub",
					"stores": []map[string]any{
						{"name": "docs", "selector": "@r1", "intent": "rw"},
					},
				},
				{
					"type":     "worker-r2",
					"executor": "stub",
					"stores": []map[string]any{
						{"name": "docs", "selector": "@r2", "intent": "rw"},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-fs-xqueue")

	const deadline = 90 * time.Second
	waitForNodeTerminal(t, ep, instanceID, "worker-r1", deadline)
	waitForNodeTerminal(t, ep, instanceID, "worker-r2", deadline)
}
