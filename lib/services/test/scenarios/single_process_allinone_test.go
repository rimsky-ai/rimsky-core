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
// through the memory blob backend across roles. Comfortably above the
// threshold so the attribute-bag JSON spills.
var singleProcessPayload = strings.Repeat("rimsky-memory-blob-roundtrip/", 300) // ~8.7 KiB

func TestSingleProcessAllInOne_MemoryBlobAcrossRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable when rimsky starts (startup
	// Capabilities handshake), so bring the network + peer up first.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	// Boot the all-in-one image on its baked SQLite default with the
	// memory blob backend, a tiny spill threshold, and an aggressive
	// orphan-sweep cadence (1s sweep / 1s retention) so the sweep's
	// cross-role reap is observable inside the test budget. Debug log
	// level makes the sweep's per-handle line visible.
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
		harness.WithBlobConfig("memory", singleProcessSpillThreshold, time.Second, time.Second),
		harness.WithContainerEnv("RIMSKY_LOG_LEVEL", "debug"),
	)
	ep := h.Endpoint

	// --- 1. Single process. The daemon-side process table (docker top;
	// no in-container ps needed against the distroless image) must show
	// exactly one rimsky process — the entrypoint itself — and zero
	// spawned role children. /health already returned 200, so the
	// synchronous migrate child has long exited and all three roles are
	// up; any rimsky-scheduler/supervisor/control-api process here would
	// mean the entrypoint still spawns children.
	assertSingleRimskyProcess(ctx, t, h)

	// --- 2 + 3 (write half). A single stub-executor node whose
	// attribute bag carries a payload above the spill threshold. The
	// dispatch bag is written to rimsky_node_attributes by the
	// supervisor role (pre-dispatch and again at terminal), spilling
	// the bytes into the memory backend.
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

	// Drive the node to terminal `fresh` through a REAL dispatch
	// (work_started event + fresh settle) — this is the all-three-roles
	// proof: scheduler enqueued, supervisor claimed and dispatched, the
	// executor ran, and the control-api served every observation.
	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	// --- 3 (read half). Read the spilled attribute back through the
	// control-api role's observability surface. The control-api opened
	// its OWN persistence driver; only the process-shared memory map
	// makes the supervisor-written blob readable here. A lost blob does
	// not error — scanAttributeRow degrades a missing handle to an empty
	// bag — so equality on the payload is the assertion that matters.
	if got := readWorkerPayload(t, ep, instanceID); got != singleProcessPayload {
		h.DumpRimskyLogs(t)
		t.Fatalf("cross-role memory-blob read-back mismatch: got %d bytes (want %d) — an empty/short payload means the control-api could not read the blob the supervisor spilled, i.e. the roles are not sharing one in-process blob map",
			len(got), len(singleProcessPayload))
	}

	// --- 4. The orphan-blob sweep reaps across roles. The terminal
	// attribute overwrite queued the pre-dispatch spill handle into
	// rimsky_blob_orphans; with 1s retention + 1s sweep interval the
	// scheduler role's next ticks must delete it from the shared map and
	// log the per-handle debug line. A failed reap (the cross-process
	// failure mode is the handle missing from a per-process map) logs
	// "reap blob orphan failed" instead.
	logs := waitForLogLine(ctx, t, h, "reaped blob orphan", 60*time.Second)
	if strings.Contains(logs, "reap blob orphan failed") {
		t.Fatalf("orphan-blob sweep logged reap failures — the scheduler role cannot delete blobs the supervisor role wrote:\n%s", logs)
	}
	if strings.Contains(logs, "SweepOrphanedBlobs failed") {
		t.Fatalf("orphan-blob sweep itself failed:\n%s", logs)
	}

	// The sweep must have reaped ONLY the orphaned prior handle: the
	// live terminal attribute is still readable afterwards.
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
