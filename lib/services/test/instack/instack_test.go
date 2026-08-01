// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package instack

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestInStack_StubExecutorDrivesNodeToFreshTerminal(t *testing.T) {
	ctx := context.Background()
	ep := harness.AcquireInStackEndpoint(ctx, t)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "instack-terminal",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": harness.InStackExecutorName,
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-instack-terminal", "instack")

	obs := ep.RequireNodeTerminalSucceeded(t, instanceID, "worker")
	if !obs.HasEventKind("work_started") {
		t.Fatalf("node %q reached fresh terminal without a work_started event — no real dispatch happened; events=%+v",
			"worker", obs.Events)
	}
}
