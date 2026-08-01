// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// @decision: topology-test-coverage
func TestSplitTopology_DriveNodeToTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimskySplit(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "split-topology",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"ok": map[string]any{"type": "boolean", "readOnly": true},
							},
						},
					},
				},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-split-topology")

	waitForDispatchToFresh(t, ep, instanceID, "worker")
}
