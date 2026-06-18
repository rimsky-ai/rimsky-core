// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stores

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestFSCrossQueueConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

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
			"name":             "fs-cross-queue",
			"version":          "1",
			"frame_timeout_ms": 600000,
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
