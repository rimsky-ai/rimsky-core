// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducers

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestFSPickVsScopeConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@r": {
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
			"name":    "fs-pick-vs-scope",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "pick-worker",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@r", "intent": "rw"},
					},
				},
				{
					"type":     "scope-worker",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "docs/alpha", "intent": "rw"},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-fs-pick-vs-scope", "claim_producers")

	const deadline = 90 * time.Second
	ep.RequireNodeTerminalSucceeded(t, instanceID, "pick-worker", deadline)
	ep.RequireNodeTerminalSucceeded(t, instanceID, "scope-worker", deadline)
}
