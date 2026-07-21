// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package instack

import (
	"context"
	"testing"
	"time"

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

	obs := ep.RequireNodeTerminalSucceeded(t, instanceID, "worker", 90*time.Second)
	if !obs.HasEventKind("work_started") {
		t.Fatalf("node %q reached fresh terminal without a work_started event — no real dispatch happened; events=%+v",
			"worker", obs.Events)
	}
}
