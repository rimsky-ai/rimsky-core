// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const singleProcessSpillThreshold = 256

var singleProcessPayload = strings.Repeat("rimsky-memory-blob-roundtrip/", 300)

func TestSingleProcessAllInOne_MemoryBlobAcrossRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
		harness.WithBlobConfig("memory", singleProcessSpillThreshold, time.Second, time.Second),
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

	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("cross-role memory-blob read-back mismatch: got %d bytes (want %d) — an empty/short payload means the control-api could not read the blob the supervisor spilled, i.e. the roles are not sharing one in-process blob map",
			len(got), len(singleProcessPayload))
	}

	logs := waitForLogLine(ctx, t, h, "reaped blob orphan", 60*time.Second)
	if strings.Contains(logs, "reap blob orphan failed") {
		t.Fatalf("orphan-blob sweep logged reap failures — the scheduler role cannot delete blobs the supervisor role wrote:\n%s", logs)
	}
	if strings.Contains(logs, "SweepOrphanedBlobs failed") {
		t.Fatalf("orphan-blob sweep itself failed:\n%s", logs)
	}

	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("attribute payload lost after orphan-blob sweep: got %d bytes (want %d) — the sweep reaped a live handle", len(got), len(singleProcessPayload))
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

func waitForLogLine(ctx context.Context, t *testing.T, h *harness.RimskyHandle, needle string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	var logs string
	for time.Now().Before(end) {
		logs = h.ReadLogs(ctx, t)
		if strings.Contains(logs, needle) {
			return logs
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container logs never contained %q within %v — the orphan-blob sweep did not reap; last logs:\n%s", needle, deadline, logs)
	return logs
}
