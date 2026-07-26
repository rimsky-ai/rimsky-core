// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducers

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestFSCrossQueueConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
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

	executorEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", executorEndpoint),
	)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "fs-cross-queue",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker-r1",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@r1", "intent": "rw"},
					},
				},
				{
					"type":     "worker-r2",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@r2", "intent": "rw"},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-fs-xqueue", "claim_producers")

	ep.RequireNodeTerminalSucceeded(t, instanceID, "worker-r1")
	ep.RequireNodeTerminalSucceeded(t, instanceID, "worker-r2")
}
