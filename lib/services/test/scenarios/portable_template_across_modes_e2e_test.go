// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const portableFilesystemConfigYAML = `root: /workspace
sweep_interval_seconds: 60
admin_port: 9120
pick_policies:
  "@docs":
    root: docs
    on_commit: recycle
    on_give_up: recycle
    visibility_timeout_seconds: 60
    sync_strategy: on_open
`

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
			"name":    "portable-template-across-modes",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "http-node",
					"claim_producers": []map[string]any{
						{"name": "store-filesystem", "selector": "@docs", "intent": "rw"},
					},
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

	hostDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostDir, "docs", "seed"), 0o755); err != nil {
		t.Fatalf("harness: seed docs folder: %v", err)
	}

	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithSQLite(),
		harness.WithContainerEnv("RIMSKY_EXECUTOR_STUB_MODE", "1"),
		harness.WithBundledFilesystemClaimProducer(hostDir, portableFilesystemConfigYAML),
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
	fs := harness.StartFilesystemStore(ctx, t, netName, "store-filesystem",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{
				{"docs", "seed"},
			},
		})

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("http-node", httpNodeEndpoint),
		harness.WithClaimProducer("store-filesystem", fs.InternalEndpoint),
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

	status, obs, raw := ep.GetNodeObservability(t, instanceID, "worker")
	if status != http.StatusOK {
		t.Fatalf("GET node observability: %d %s", status, string(raw))
	}
	var latestAttrs struct {
		Stub bool `json:"stub"`
	}
	if err := json.Unmarshal(obs.LatestAttributes, &latestAttrs); err != nil {
		t.Fatalf("decode latest_attributes: %v: %s", err, string(raw))
	}
	if !latestAttrs.Stub {
		t.Fatalf("stub outcome missing from latest_attributes; body=%s", string(raw))
	}
	return portableTerminalShape{
		NodeType:         obs.NodeType,
		TerminalTagClass: terminalTagFromRunSummary(obs.RunSummary.FreshCount, obs.RunSummary.FailedCount),
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
