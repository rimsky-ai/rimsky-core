// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const validatorPlaintextToken = "validator-plaintext-token-do-not-leak-9e7f7c2a"

const expectedAuthorizationHeader = "Bearer " + validatorPlaintextToken

func TestClaudeAgentCrossStack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pubPEM, privPEM := mustGenerateEd25519PEMs(t)

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake",
		harness.ClaudeAgentFakeOptions{
			McpAllowlist:         []string{"validator"},
			ExposeEnvAllowlist:   []string{"VALIDATOR_TOKEN"},
			SignoffPrivateKeyPEM: privPEM,
			ExtraEnv: map[string]string{
				"VALIDATOR_TOKEN": validatorPlaintextToken,
			},
		},
	)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
	)

	t.Run("signoff gate accepts signed bound output", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-signoff-ok",
			"scenario:signoff_ok",
			withSignoffGate(pubPEM, "endpoints", 0),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-signoff-ok")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "fresh", 90*time.Second)
	})

	t.Run("signoff gate rejects unsigned bound output", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-signoff-missing",
			"scenario:signoff_missing",
			withSignoffGate(pubPEM, "endpoints", 1),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-signoff-missing")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", 90*time.Second)
	})

	t.Run("mcp server outside the operator allowlist is refused", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-mcp-refused",
			"scenario:signoff_ok",
			withInlineMcpServer("inline-bad", "http://example.invalid/mcp"),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-mcp-refused")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", 90*time.Second)
	})

	t.Run("expose-env name outside the operator allowlist is refused", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-expose-env-refused",
			"scenario:signoff_ok",
			withExposeEnv("FORBIDDEN_SECRET"),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-expose-env-refused")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", 90*time.Second)
	})

	t.Run("upstream rate-limit (stderr + non-zero exit) parks the node as snooze", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-rate-limited",
			"scenario:rate_limited",
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-rate-limited")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeParkedSnoozeClaudeAgent(t, ep, nodeID)
	})

	t.Run("expose-env allowlist reaches the agent; rimsky never sees the plaintext", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-env-ref-witness",
			"scenario:env_ref_witness",
			withSignoffGate(pubPEM, "cli_observation", 0),
			withInlineMcpServer("validator", "http://127.0.0.1:9999/mcp"),
			withExposeEnv("VALIDATOR_TOKEN"),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-env-ref-witness")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "fresh", 120*time.Second)

		// @story: claude-agent
		bag := getLatestAttributesClaudeAgent(t, ep, nodeID)
		obs, ok := bag["cli_observation"].(map[string]any)
		if !ok {
			t.Fatalf("latest_attributes missing cli_observation: %v", bag)
		}
		gotDigest, _ := obs["validator_header_digest_sha256"].(string)
		wantDigest := sha256HexClaudeAgent(expectedAuthorizationHeader)
		if gotDigest != wantDigest {
			t.Fatalf("validator header digest mismatch: got %q, want %q — the expose-env allowlist didn't deliver VALIDATOR_TOKEN to the agent's own env, or the agent didn't construct the expected Bearer header from it",
				gotDigest, wantDigest)
		}

		// @story: claude-agent
		raw, err := json.Marshal(bag)
		if err != nil {
			t.Fatalf("re-marshal latest_attributes: %v", err)
		}
		if strings.Contains(string(raw), validatorPlaintextToken) {
			t.Fatalf("plaintext token %q leaked into rimsky-persisted node attributes: %s",
				validatorPlaintextToken, string(raw))
		}

		// @story: claude-agent
		cli, _ := bag["cli"].(map[string]any)
		servers, _ := cli["mcp_servers"].([]any)
		if len(servers) != 1 {
			t.Fatalf("expected one cli.mcp_servers entry, got %d: %v", len(servers), servers)
		}
		first, _ := servers[0].(map[string]any)
		if got, _ := first["name"].(string); got != "validator" {
			t.Fatalf("persisted cli.mcp_servers[0] missing name=validator (got %v) — the dispatch attribute was rewritten, leaking through a different surface",
				first)
		}
		if got, _ := first["transport"].(string); got != "http" {
			t.Fatalf("persisted cli.mcp_servers[0] missing transport=http (got %v)", first)
		}
	})
}

type claudeAgentTemplateOption func(workerNodeAttrs map[string]any)

func buildClaudeAgentTemplate(name, userPrompt string, opts ...claudeAgentTemplateOption) map[string]any {
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
					"default": "you are a cross-stack proof stub. follow the scenario hint in the user prompt verbatim.",
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
	for _, opt := range opts {
		opt(attrs)
	}
	return map[string]any{
		"spec": map[string]any{
			"name":             name,
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":       "worker",
					"executor":   "claude-agent",
					"attributes": attrs,
				},
			},
		},
	}
}

func withSignoffGate(publicKeyPEM, path string, maxAttempts int) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		entry := map[string]any{
			"public_key": publicKeyPEM,
		}
		if path != "" {
			entry["path"] = path
		}
		cliDefault["required_signoffs"] = []any{entry}
		if maxAttempts > 0 {
			cliDefault["max_signoff_attempts"] = maxAttempts
		}
	}
}

func withInlineMcpServer(name, url string) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		cliDefault["mcp_servers"] = []any{
			map[string]any{"transport": "http", "name": name, "url": url},
		}
	}
}

func withExposeEnv(names ...string) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		exposeEnv := make([]any, 0, len(names))
		for _, n := range names {
			exposeEnv = append(exposeEnv, n)
		}
		cliDefault["expose_env"] = exposeEnv
	}
}

func waitNodeSettledClaudeAgent(
	t *testing.T,
	ep harness.RimskyEndpoint,
	nodeID string,
	wantState string,
	deadline time.Duration,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState string
		lastBody  string
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/nodes/"+nodeID, "")
		if status == http.StatusOK {
			var resp struct {
				RunSummary struct {
					ActiveCount  int `json:"active_count"`
					PendingCount int `json:"pending_count"`
					FreshCount   int `json:"fresh_count"`
					FailedCount  int `json:"failed_count"`
				} `json:"run_summary"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = categorizeRunSummary(resp.RunSummary.ActiveCount, resp.RunSummary.PendingCount, resp.RunSummary.FreshCount, resp.RunSummary.FailedCount)
				if wantState == "fresh" && lastState == "fresh" {
					if hasWorkStartedEvent(t, ep, nodeID) {
						return
					}
				}
				if wantState == "failed" && lastState == "failed" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %s did not settle to categorical state=%q within %v; last_state=%q last_body=%s",
		nodeID, wantState, deadline, lastState, lastBody)
}

func waitNodeParkedSnoozeClaudeAgent(t *testing.T, ep harness.RimskyEndpoint, nodeID string) {
	t.Helper()
	for {
		status, raw := ep.GetJSON(t, "/v1/diagnostics/parked?reason=snooze", "")
		if status == http.StatusOK {
			var resp struct {
				ParkedNodes []struct {
					NodeID string `json:"node_id"`
					Reason string `json:"reason"`
				} `json:"parked_nodes"`
			}
			if json.Unmarshal(raw, &resp) == nil {
				for _, p := range resp.ParkedNodes {
					if p.NodeID == nodeID && p.Reason == "snooze" {
						return
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// @concept: node
func categorizeRunSummary(active, pending, fresh, failed int) string {
	if failed > 0 {
		return "failed"
	}
	if active > 0 || pending > 0 {
		return "in-flight"
	}
	if fresh > 0 {
		return "fresh"
	}
	return "idle"
}

func hasWorkStartedEvent(t *testing.T, ep harness.RimskyEndpoint, nodeID string) bool {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/nodes/"+nodeID, "")
	if status != http.StatusOK {
		return false
	}
	var nodeResp struct {
		InstanceID string `json:"instance_id"`
		NodeType   string `json:"node_type"`
	}
	if err := json.Unmarshal(raw, &nodeResp); err != nil {
		return false
	}
	if nodeResp.InstanceID == "" || nodeResp.NodeType == "" {
		return false
	}
	statusE, rawE := ep.GetJSON(t,
		fmt.Sprintf("/v1/observability/nodes/%s/%s", nodeResp.InstanceID, nodeResp.NodeType), "")
	if statusE != http.StatusOK {
		return false
	}
	var eResp struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rawE, &eResp); err != nil {
		return false
	}
	for _, e := range eResp.Events {
		if e.Kind == "work_started" {
			return true
		}
	}
	return false
}

func getLatestAttributesClaudeAgent(t *testing.T, ep harness.RimskyEndpoint, nodeID string) map[string]any {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/nodes/"+nodeID, "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/nodes/%s: %d %s", nodeID, status, string(raw))
	}
	var resp struct {
		LatestAttributes map[string]any `json:"latest_attributes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode latest_attributes: %v: %s", err, string(raw))
	}
	if resp.LatestAttributes == nil {
		t.Fatalf("latest_attributes missing on node %s: %s", nodeID, string(raw))
	}
	return resp.LatestAttributes
}

func sha256HexClaudeAgent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func mustGenerateEd25519PEMs(t *testing.T) (pubPEM, privPEM string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	return pubPEM, privPEM
}
