// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: single-process-all-in-one
// @decision: bundled-registry-entrypoint

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestBundledInProcDispatchZeroExecutorConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithContainerEnv("RIMSKY_EXECUTOR_STUB_MODE", "1"),
	)
	ep := h.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpRimskyLogs(t)
		}
	})

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "bundled-inproc-dispatch",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "http-node",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"stub_probe": map[string]any{
									"type":    "boolean",
									"default": true,
								},
								"stub": map[string]any{
									"type":    "boolean",
									"default": false,
								},
							},
						},
					},
				},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-bundled-inproc-dispatch")

	waitForDispatchToFresh(t, ep, instanceID, "worker")

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
	if got, _ := resp.LatestAttributes["stub"].(bool); !got {
		t.Fatalf("in-proc http-node stub outcome never landed: the bundled handler's attributes_delta {stub: true} is missing from latest_attributes %v — the template named no configured executor, so this only passes if the in-proc bundled registration resolved and dispatched it", resp.LatestAttributes)
	}

	// @story: single-process-all-in-one
	statusServices, rawServices := ep.GetJSON(t, "/v1/observability/executors", "")
	if statusServices != http.StatusOK {
		t.Fatalf("GET /v1/observability/executors: %d %s", statusServices, string(rawServices))
	}
	var serviceResp struct {
		Executors []struct {
			Name     string `json:"name"`
			Endpoint string `json:"endpoint"`
			Static   bool   `json:"static"`
		} `json:"executors"`
	}
	if err := json.Unmarshal(rawServices, &serviceResp); err != nil {
		t.Fatalf("decode service list: %v: %s", err, string(rawServices))
	}
	var httpNode *struct {
		Name     string `json:"name"`
		Endpoint string `json:"endpoint"`
		Static   bool   `json:"static"`
	}
	for i := range serviceResp.Executors {
		if serviceResp.Executors[i].Name == "http-node" {
			httpNode = &serviceResp.Executors[i]
			break
		}
	}
	if httpNode == nil {
		t.Fatalf("http-node executor missing from observability service list %s — the bundled adverts did not populate the discovery cache", string(rawServices))
	}
	if !httpNode.Static {
		t.Fatalf("http-node service entry is not marked static (%+v) — the dispatch was fielded via an external service process, not the in-proc bundled handler", *httpNode)
	}
	if !strings.HasPrefix(httpNode.Endpoint, "inproc://") {
		t.Fatalf("http-node service entry has non-inproc endpoint %q — the dispatch was fielded via an external service process reachable at that address, not the in-proc bundled handler", httpNode.Endpoint)
	}
}
