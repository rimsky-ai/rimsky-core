// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// @story: portable-template-across-modes
func TestPortableTemplateAcrossModes(t *testing.T) {
	t.Parallel()

	templateBody := buildPortableTemplateBody()
	templateBytes, err := json.Marshal(templateBody)
	if err != nil {
		t.Fatalf("marshal template body: %v", err)
	}

	shapeAllInOne := runPortableTemplateInProc(t, templateBytes)
	shapeContainerized := runPortableTemplateContainerized(t, templateBytes)

	if shapeAllInOne.NodeType != shapeContainerized.NodeType {
		t.Fatalf("terminal node_type differs across modes: all-in-one=%q containerized=%q — same template file byte-for-byte reached a different terminal shape",
			shapeAllInOne.NodeType, shapeContainerized.NodeType)
	}
	if shapeAllInOne.TerminalTagClass != shapeContainerized.TerminalTagClass {
		t.Fatalf("terminal tag class differs across modes: all-in-one=%q containerized=%q — same template file byte-for-byte reached a different terminal tag",
			shapeAllInOne.TerminalTagClass, shapeContainerized.TerminalTagClass)
	}
}

type portableTerminalShape struct {
	NodeType         string
	TerminalTagClass string
}

func buildPortableTemplateBody() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":             "portable-template-across-modes",
			"version":          "1",
			"frame_timeout_ms": 600000,
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
	}
}

func runPortableTemplateInProc(t *testing.T, templateBytes []byte) portableTerminalShape {
	t.Helper()
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

	return dispatchAndReadTerminal(t, ep, templateBytes, "ck-portable-template-inproc")
}

func runPortableTemplateContainerized(t *testing.T, templateBytes []byte) portableTerminalShape {
	t.Helper()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	httpNodeEndpoint := harness.StartHttpNodeStubOnNetwork(ctx, t, netName, "http-node")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("http-node", httpNodeEndpoint),
	)

	return dispatchAndReadTerminal(t, ep, templateBytes, "ck-portable-template-containerized")
}

func dispatchAndReadTerminal(t *testing.T, ep harness.RimskyEndpoint, templateBytes []byte, instanceKey string) portableTerminalShape {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(templateBytes, &body); err != nil {
		t.Fatalf("re-decode template bytes: %v", err)
	}
	templateID := deployScenarioTemplate(t, ep, body)
	instanceID := createScenarioInstance(t, ep, templateID, instanceKey)

	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/worker", "")
	if status != http.StatusOK {
		t.Fatalf("GET node observability: %d %s", status, string(raw))
	}
	var resp struct {
		NodeType         string `json:"node_type"`
		LatestAttributes struct {
			Stub bool `json:"stub"`
		} `json:"latest_attributes"`
		RunSummary struct {
			FreshCount  int `json:"fresh_count"`
			FailedCount int `json:"failed_count"`
		} `json:"run_summary"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode node observability: %v: %s", err, string(raw))
	}
	if !resp.LatestAttributes.Stub {
		t.Fatalf("stub outcome missing from latest_attributes; body=%s", string(raw))
	}
	return portableTerminalShape{
		NodeType:         resp.NodeType,
		TerminalTagClass: terminalTagFromRunSummary(resp.RunSummary.FreshCount, resp.RunSummary.FailedCount),
	}
}

func terminalTagFromRunSummary(fresh, failed int) string {
	if failed > 0 {
		return "failed"
	}
	if fresh > 0 {
		return "fresh"
	}
	return "unknown"
}
