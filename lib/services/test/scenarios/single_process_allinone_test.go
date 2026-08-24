// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

var singleProcessPayload = strings.Repeat("rimsky-all-in-one-roundtrip/", 4000)

// @story: single-process-all-in-one
// @decision: attribute-bytes-in-the-row
func TestSingleProcessAllInOne_OneProcessServesAllThreeRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
		harness.WithContainerEnv("RIMSKY_LOG_LEVEL", "debug"),
	)
	ep := h.Endpoint

	assertSingleRimskyProcess(ctx, t, h)

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "single-process-all-in-one",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"payload": map[string]any{
									"type":    "string",
									"default": singleProcessPayload,
								},
							},
						},
					},
				},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-single-process-all-in-one")

	waitForDispatchToFresh(t, ep, instanceID, "worker")

	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("cross-role attribute read-back mismatch: got %d bytes, want %d — the control-api role did not read back the bag the supervisor role wrote",
			len(got), len(singleProcessPayload))
	}
}

func assertSingleRimskyProcess(ctx context.Context, t *testing.T, h *harness.RimskyHandle) {
	t.Helper()
	procs := h.TopProcesses(ctx, t)
	var entrypoints, roleChildren, rimskyTotal int
	var lines []string
	for _, p := range procs {
		line := strings.Join(p, " ")
		lines = append(lines, line)
		if !strings.Contains(line, "rimsky") {
			continue
		}
		rimskyTotal++
		switch {
		case strings.Contains(line, "rimsky-entrypoint"):
			entrypoints++
		case strings.Contains(line, "rimsky-scheduler"),
			strings.Contains(line, "rimsky-supervisor"),
			strings.Contains(line, "rimsky-control-api"),
			strings.Contains(line, "rimsky-migrate"):
			roleChildren++
		}
	}
	table := strings.Join(lines, "\n")
	if entrypoints != 1 {
		t.Fatalf("want exactly 1 rimsky-entrypoint process, got %d; process table:\n%s", entrypoints, table)
	}
	if roleChildren != 0 {
		t.Fatalf("want 0 spawned role-child processes (the all-in-one must run roles in-process), got %d; process table:\n%s", roleChildren, table)
	}
	if rimskyTotal != 1 {
		t.Fatalf("want exactly 1 rimsky process total, got %d; process table:\n%s", rimskyTotal, table)
	}
}

func readWorkerPayload(t *testing.T, ep harness.RimskyEndpoint, instanceID string) string {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/worker", "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/observability/nodes/%s/worker: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		LatestAttributes map[string]any `json:"latest_attributes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode node observability response: %v: %s", err, string(raw))
	}
	payload, _ := resp.LatestAttributes["payload"].(string)
	return payload
}
