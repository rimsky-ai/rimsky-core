// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @story: bundled-park-resume-recipe
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const parkRatelimitImage = "rimsky-example/park-ratelimiter"

func TestParkThenResumeOnBundledStackE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	startParkRatelimitOnNetwork(ctx, t, netName, "park-ratelimit")

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithContainerEnv("RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST", "0.0.0.0/0"),
	)
	ep := h.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpRimskyLogs(t)
		}
	})

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "park-resume-recipe",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "fetch-report",
					"executor": "http-node",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"url": map[string]any{
									"type":    "string",
									"default": "http://park-ratelimit:9420/report",
								},
							},
						},
					},
				},
			},
		},
	})
	instanceID := ep.CreateInstance(t, templateID, "ck-park-resume", "park-resume")

	waitForResumedSuccess(t, ep, instanceID, "fetch-report")

	parkEvents := listInstanceEvents(t, ep, instanceID, "transient/park")
	if len(parkEvents) == 0 {
		t.Fatalf("no transient/park audit event for instance %s — the 429 dispatch did not travel the production parking path", instanceID)
	}
	successEvents := listInstanceEvents(t, ep, instanceID, "terminal/success")
	if len(successEvents) == 0 {
		t.Fatalf("no terminal/success audit event for instance %s — the parked run never resumed to a successful settlement", instanceID)
	}
}

func waitForResumedSuccess(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) {
	t.Helper()
	obs := ep.PollNodeObservability(t, instanceID, nodeType, func(o harness.NodeObservability) bool {
		return o.RunSummary.FailedCount > 0 ||
			(o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0)
	})
	if obs.RunSummary.FailedCount > 0 {
		t.Fatalf("node %q settled failed, want a resumed success; run summary %+v", nodeType, obs.RunSummary)
	}
}

func listInstanceEvents(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind string) []map[string]any {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/observability/events?instance_id="+instanceID+"&kind="+kind, "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/observability/events (kind=%s): %d %s", kind, status, string(raw))
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode events response: %v: %s", err, string(raw))
	}
	return body.Events
}

func startParkRatelimitOnNetwork(ctx context.Context, t *testing.T, networkName, alias string) {
	t.Helper()
	c, err := harness.Run(ctx, harness.ImageRef(parkRatelimitImage),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{"RATELIMIT_RETRY_AFTER_SECONDS": "2"}),
		testcontainers.WithExposedPorts("9420/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9420/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start example park-ratelimit endpoint: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
}
