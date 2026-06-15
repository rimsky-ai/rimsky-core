// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Standing proof for the three-container split topology
// (TD-topology-test-coverage): one container per role from the
// `rimsky:latest` image (command: [rimsky-scheduler] /
// [rimsky-supervisor] / [rimsky-control-api]) against a shared
// Postgres, driven through the same register → deploy → create →
// terminal scenario as the single-process all-in-one proof. The
// single-process rework changed the default deployment's process model;
// this test pins that the explicit-role-per-container contract — the
// other supported topology — still orchestrates end to end, including
// the migrate-once rule (the control-api container migrates; scheduler
// and supervisor boot against the schema it created).
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestSplitTopology_DriveNodeToTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: stub executor must be reachable when the roles start
	// (startup Capabilities handshake), so bring the network + peer up first.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimskySplit(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// @deliberate: same scenario as the single-process proof — a single
	// stub-executor node with an attribute bag, driven to the terminal
	// `fresh` state through a REAL dispatch (work_started + fresh settle).
	// Proves the scheduler container enqueued, the supervisor container
	// claimed and dispatched, the executor ran, and the control-api
	// container served every observation, all against the shared Postgres.
	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "split-topology",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
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

	waitForDispatchToFresh(t, ep, instanceID, "worker", 120*time.Second)
}
