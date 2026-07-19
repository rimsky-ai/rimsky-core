// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const (
	perNodeReviewerSeed = "reviewer-seed-plaintext-do-not-leak-3b2d1e64"
	perNodeAlphaMcpURL  = "http://127.0.0.1:9999/mcp/alpha"
	perNodeBetaMcpURL   = "http://127.0.0.1:9999/mcp/beta"
	perNodeModuleSpecs  = "scenario:per-node-module-witness"
	perNodeModuleServer = "module-witness"
	moduleWitnessTextOK = "module-transport-reached"
)

// @story: claude-agent-mcp-servers-per-node
// @story: claude-agent-expose-env-per-node
func TestClaudeAgentPerNodeDivergence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pubPEM, privPEM := mustGenerateEd25519PEMs(t)

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake-per-node",
		harness.ClaudeAgentFakeOptions{
			McpAllowlist:         []string{"validator", "local-tool", perNodeModuleServer},
			ExposeEnvAllowlist:   []string{"VALIDATOR_TOKEN", "REVIEWER_SEED"},
			SignoffPrivateKeyPEM: privPEM,
			ExtraEnv: map[string]string{
				"VALIDATOR_TOKEN": validatorPlaintextToken,
				"REVIEWER_SEED":   perNodeReviewerSeed,
			},
		},
	)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
	)

	tid := deployScenarioTemplate(t, ep, buildPerNodeTemplate(pubPEM))
	iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-per-node")

	alphaID := resolveWorkerNodeID(t, ep, iid, "worker-alpha")
	betaID := resolveWorkerNodeID(t, ep, iid, "worker-beta")
	gammaID := resolveWorkerNodeID(t, ep, iid, "worker-gamma")

	waitNodeSettledClaudeAgent(t, ep, alphaID, "fresh", 120*time.Second)
	waitNodeSettledClaudeAgent(t, ep, betaID, "fresh", 120*time.Second)
	waitNodeSettledClaudeAgent(t, ep, gammaID, "fresh", 120*time.Second)

	alphaObs := readPerNodeObservation(t, ep, alphaID)
	betaObs := readPerNodeObservation(t, ep, betaID)
	gammaObs := readPerNodeObservation(t, ep, gammaID)

	// @story: claude-agent-mcp-servers-per-node
	assertMcpServer(t, "worker-alpha", alphaObs, "validator", "http")
	if hasMcpServer(alphaObs, "local-tool") {
		t.Fatalf("worker-alpha observed local-tool in its mcp config — per-node MCP declarations are leaking across nodes; alpha bag=%v", alphaObs)
	}
	if hasMcpServer(alphaObs, perNodeModuleServer) {
		t.Fatalf("worker-alpha observed %s in its mcp config — per-node MCP declarations are leaking across nodes; alpha bag=%v", perNodeModuleServer, alphaObs)
	}
	assertMcpServer(t, "worker-beta", betaObs, "local-tool", "http")
	if hasMcpServer(betaObs, "validator") {
		t.Fatalf("worker-beta observed validator in its mcp config — per-node MCP declarations are leaking across nodes; beta bag=%v", betaObs)
	}
	if hasMcpServer(betaObs, perNodeModuleServer) {
		t.Fatalf("worker-beta observed %s in its mcp config — per-node MCP declarations are leaking across nodes; beta bag=%v", perNodeModuleServer, betaObs)
	}

	// @story: claude-agent-mcp-servers-per-node
	assertMcpServer(t, "worker-gamma", gammaObs, perNodeModuleServer, "http")
	if hasMcpServer(gammaObs, "validator") {
		t.Fatalf("worker-gamma observed validator in its mcp config — per-node MCP declarations are leaking across nodes; gamma bag=%v", gammaObs)
	}
	if hasMcpServer(gammaObs, "local-tool") {
		t.Fatalf("worker-gamma observed local-tool in its mcp config — per-node MCP declarations are leaking across nodes; gamma bag=%v", gammaObs)
	}
	gammaWitnessResult, _ := gammaObs["module_witness_result"].(string)
	if gammaWitnessResult != moduleWitnessTextOK {
		t.Fatalf("worker-gamma module transport not reached: module_witness_result = %q, want %q — the CLI child called the module-loopback MCP server's own tool and did not get back the expected witness text, so the module (http-loopback) transport did not resolve to a live server",
			gammaWitnessResult, moduleWitnessTextOK)
	}

	// @story: claude-agent-expose-env-per-node
	assertEnvPresent(t, "worker-alpha", alphaObs, "VALIDATOR_TOKEN", validatorPlaintextToken)
	assertEnvAbsent(t, "worker-alpha", alphaObs, "REVIEWER_SEED")
	assertEnvPresent(t, "worker-beta", betaObs, "REVIEWER_SEED", perNodeReviewerSeed)
	assertEnvAbsent(t, "worker-beta", betaObs, "VALIDATOR_TOKEN")
	assertEnvAbsent(t, "worker-gamma", gammaObs, "VALIDATOR_TOKEN")
	assertEnvAbsent(t, "worker-gamma", gammaObs, "REVIEWER_SEED")

	assertNoPlaintext(t, "worker-alpha", ep, alphaID)
	assertNoPlaintext(t, "worker-beta", ep, betaID)
	assertNoPlaintext(t, "worker-gamma", ep, gammaID)
}

func buildPerNodeTemplate(pubPEM string) map[string]any {
	alphaAttrs := perNodeAgentAttrs("scenario:per_node_witness node_hint alpha", pubPEM)
	setInlineMcpHTTP(alphaAttrs, "validator", perNodeAlphaMcpURL)
	setExposeEnv(alphaAttrs, "VALIDATOR_TOKEN")

	betaAttrs := perNodeAgentAttrs("scenario:per_node_witness node_hint beta", pubPEM)
	setInlineMcpHTTP(betaAttrs, "local-tool", perNodeBetaMcpURL)
	setExposeEnv(betaAttrs, "REVIEWER_SEED")

	gammaAttrs := perNodeAgentAttrs("scenario:per_node_witness node_hint gamma", pubPEM)
	setInlineMcpModule(gammaAttrs, perNodeModuleServer, perNodeModuleSpecs)

	return map[string]any{
		"spec": map[string]any{
			"name":    "claude-agent-per-node-divergence",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":       "worker-alpha",
					"executor":   "claude-agent",
					"attributes": alphaAttrs,
				},
				{
					"type":       "worker-beta",
					"executor":   "claude-agent",
					"attributes": betaAttrs,
				},
				{
					"type":       "worker-gamma",
					"executor":   "claude-agent",
					"attributes": gammaAttrs,
				},
			},
		},
	}
}

func perNodeAgentAttrs(userPrompt, pubPEM string) map[string]any {
	attrs := map[string]any{
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model": map[string]any{
					"type":    "string",
					"default": "claude-sonnet-4-5",
				},
				"system_prompt": map[string]any{
					"type":    "string",
					"default": "per-node divergence proof stub. inspect --mcp-config and env, then report.",
				},
				"user_prompt": map[string]any{
					"type":    "string",
					"default": userPrompt,
				},
				"cli": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
					"default":    map[string]any{},
				},
			},
		},
	}
	cliDefault := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)["default"].(map[string]any)
	cliDefault["required_signoffs"] = []any{map[string]any{
		"public_key": pubPEM,
		"path":       "cli_observation",
	}}
	return attrs
}

func setInlineMcpHTTP(attrs map[string]any, name, url string) {
	cliDefault := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)["default"].(map[string]any)
	cliDefault["mcp_servers"] = []any{
		map[string]any{"transport": "http", "name": name, "url": url},
	}
}

func setInlineMcpModule(attrs map[string]any, name, module string) {
	cliDefault := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)["default"].(map[string]any)
	cliDefault["mcp_servers"] = []any{
		map[string]any{"transport": "module", "name": name, "module": module},
	}
}

func setExposeEnv(attrs map[string]any, names ...string) {
	cliDefault := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)["default"].(map[string]any)
	exposeEnv := make([]any, 0, len(names))
	for _, n := range names {
		exposeEnv = append(exposeEnv, n)
	}
	cliDefault["expose_env"] = exposeEnv
}

func readPerNodeObservation(t *testing.T, ep harness.RimskyEndpoint, nodeID string) map[string]any {
	t.Helper()
	bag := getLatestAttributesClaudeAgent(t, ep, nodeID)
	obs, ok := bag["cli_observation"].(map[string]any)
	if !ok {
		t.Fatalf("node %s: latest_attributes missing cli_observation: %v", nodeID, bag)
	}
	return obs
}

func hasMcpServer(obs map[string]any, name string) bool {
	servers, _ := obs["mcp_servers"].([]any)
	for _, s := range servers {
		entry, _ := s.(map[string]any)
		if got, _ := entry["name"].(string); got == name {
			return true
		}
	}
	return false
}

func assertMcpServer(t *testing.T, nodeLabel string, obs map[string]any, wantName, wantTransport string) {
	t.Helper()
	servers, _ := obs["mcp_servers"].([]any)
	for _, s := range servers {
		entry, _ := s.(map[string]any)
		name, _ := entry["name"].(string)
		if name != wantName {
			continue
		}
		if got, _ := entry["transport"].(string); got != wantTransport {
			t.Fatalf("%s: mcp server %q transport = %q, want %q — the executor rewrote the transport or the per-node declaration didn't reach the child's mcp config",
				nodeLabel, wantName, got, wantTransport)
		}
		return
	}
	t.Fatalf("%s: mcp server %q missing from observed mcp_servers %v — the per-node declaration didn't reach the child's mcp config",
		nodeLabel, wantName, servers)
}

func assertEnvPresent(t *testing.T, nodeLabel string, obs map[string]any, name, plaintext string) {
	t.Helper()
	presence, _ := obs["env_presence"].(map[string]any)
	if got, _ := presence[name].(bool); !got {
		t.Fatalf("%s: env var %q was NOT visible in the CLI child (env_presence=%v) — the expose-env intersection did not pass it through",
			nodeLabel, name, presence)
	}
	digests, _ := obs["env_digests"].(map[string]any)
	wantDigest := sha256HexClaudeAgent(plaintext)
	if got, _ := digests[name].(string); got != wantDigest {
		t.Fatalf("%s: env var %q digest mismatch: got %q want %q — the CLI child saw a different value than the operator env carried",
			nodeLabel, name, got, wantDigest)
	}
}

func assertEnvAbsent(t *testing.T, nodeLabel string, obs map[string]any, name string) {
	t.Helper()
	presence, _ := obs["env_presence"].(map[string]any)
	if got, _ := presence[name].(bool); got {
		t.Fatalf("%s: env var %q leaked into the CLI child (env_presence=%v) — the per-node expose-env list didn't scope the exposure",
			nodeLabel, name, presence)
	}
}

func assertNoPlaintext(t *testing.T, nodeLabel string, ep harness.RimskyEndpoint, nodeID string) {
	t.Helper()
	bag := getLatestAttributesClaudeAgent(t, ep, nodeID)
	raw, err := json.Marshal(bag)
	if err != nil {
		t.Fatalf("%s: re-marshal latest_attributes: %v", nodeLabel, err)
	}
	for _, secret := range []string{validatorPlaintextToken, perNodeReviewerSeed} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s: plaintext env value leaked into rimsky-persisted node attributes: %s",
				nodeLabel, string(raw))
		}
	}
}
