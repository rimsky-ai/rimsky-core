// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable proof for STORY-single-process-all-in-one: the all-in-one
// deployment runs all three roles (scheduler, supervisor, control-api)
// in ONE OS process, and because the roles genuinely share a process the
// in-process "memory" blob backend works across role boundaries.
//
// The test boots `rimsky-all-in-one:latest` (no role command — the
// single-process path) with the memory blob backend and a low spill
// threshold, then proves each leg of the story:
//
//  1. Single process: the Docker daemon's process table for the
//     container shows exactly one rimsky-entrypoint process and ZERO
//     spawned role children — the roles run in-process, not as the old
//     three-child-process stack.
//  2. All three role surfaces from that one process: the control-api
//     serves the HTTP surface, the scheduler enqueues, the supervisor
//     claims/dispatches — proven by driving a node through a real stub
//     dispatch to the terminal `fresh` state.
//  3. Cross-role memory-blob round-trip: a node attribute larger than
//     the spill threshold is spilled to the memory backend by the
//     SUPERVISOR role's attribute write, then read back through the
//     CONTROL-API role's observability surface. If the roles did not
//     share one process (one in-process blob map), the read would miss
//     and surface an empty attribute bag (the scanAttributeRow
//     missing-blob fallback), so an intact read-back is the
//     discriminating evidence of the shared process.
//  4. The orphan-blob sweep actually reaps: the terminal attribute
//     overwrite orphans the pre-dispatch spill handle; the SCHEDULER
//     role's sweep deletes it from the shared map (visible as the
//     sweep's per-handle debug line, with no reap failures), and the
//     live handle survives the sweep — read-back still intact after.
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

// singleProcessSpillThreshold is the configured spill cutoff in bytes.
// Deliberately tiny so an ordinary string attribute spills.
const singleProcessSpillThreshold = 256

// singleProcessPayload is the attribute value that must round-trip
// through the memory blob backend across roles. Sized at ~8.7 KiB,
// comfortably above the threshold so the attribute-bag JSON spills.
var singleProcessPayload = strings.Repeat("rimsky-memory-blob-roundtrip/", 300)

func TestSingleProcessAllInOne_MemoryBlobAcrossRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: stub executor must be reachable when rimsky starts
	// (startup Capabilities handshake), so bring the network + peer up
	// before BringUpRimskyHandle.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	// @deliberate: 1s/1s sweep cadence + debug log level make the
	// cross-role orphan-blob reap observable inside the test budget; the
	// tiny spill threshold forces ordinary string attributes to spill.
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
		harness.WithBlobConfig("memory", singleProcessSpillThreshold, time.Second, time.Second),
		harness.WithContainerEnv("RIMSKY_LOG_LEVEL", "debug"),
	)
	ep := h.Endpoint

	// @constraint: /health already returned 200 so the synchronous
	// migrate child has long exited and all three roles are up; any
	// rimsky-scheduler/supervisor/control-api process visible here would
	// mean the entrypoint still spawns children rather than running the
	// roles in-process.
	assertSingleRimskyProcess(ctx, t, h)

	// @deliberate: phase 2+3 (write half) — the dispatch bag is written to rimsky_node_attributes by the supervisor role and SPILLS the bytes into the memory blob backend (legal only because RIMSKY_PROCESS_ROLE=unified means scheduler/supervisor/control-api share one in-process map; per-role processes cannot).
	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "single-process-all-in-one",
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

	// @constraint: scanAttributeRow silently degrades a missing blob
	// handle to an empty bag (no error), so payload-equality is the
	// discriminating assertion — the control-api role opened its OWN
	// persistence driver, and only the process-shared in-process blob
	// map makes the supervisor-written blob readable here.
	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("cross-role memory-blob read-back mismatch: got %d bytes (want %d) — an empty/short payload means the control-api could not read the blob the supervisor spilled, i.e. the roles are not sharing one in-process blob map",
			len(got), len(singleProcessPayload))
	}

	// @constraint: cross-role reap is the load-bearing assertion. The
	// terminal attribute overwrite queued the pre-dispatch spill handle
	// into rimsky_blob_orphans; the scheduler role's sweep must delete
	// it from the shared in-process map. The cross-process failure mode
	// (handle missing from a per-process map) surfaces as
	// "reap blob orphan failed" rather than a successful "reaped blob
	// orphan" line — both are checked below.
	logs := waitForLogLine(ctx, t, h, "reaped blob orphan", 60*time.Second)
	if strings.Contains(logs, "reap blob orphan failed") {
		t.Fatalf("orphan-blob sweep logged reap failures — the scheduler role cannot delete blobs the supervisor role wrote:\n%s", logs)
	}
	if strings.Contains(logs, "SweepOrphanedBlobs failed") {
		t.Fatalf("orphan-blob sweep itself failed:\n%s", logs)
	}

	// @constraint: sweep must reap ONLY the orphaned prior handle —
	// re-reading after the sweep proves the live terminal attribute
	// survived (a sweep that nuked the live handle would degrade to an
	// empty bag via the same scanAttributeRow path above).
	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("attribute payload lost after orphan-blob sweep: got %d bytes (want %d) — the sweep reaped a live handle", len(got), len(singleProcessPayload))
	}
}

// assertSingleRimskyProcess asserts the container's process table holds
// exactly one rimsky-entrypoint process and no spawned role children.
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

// readWorkerPayload reads the worker node's latest attribute bag through
// the control-api observability surface and returns its "payload" value
// ("" when absent — the missing-blob degradation shape).
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

// waitForLogLine polls the container logs until they contain `needle` or
// the deadline elapses, returning the final log snapshot either way (the
// caller runs its negative assertions over the same snapshot).
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
