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

	catalogYAML := `validator:
  transport: http
  url: http://127.0.0.1:9999/mcp
  headers:
    Authorization: "Bearer ${env:VALIDATOR_TOKEN}"
`

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake",
		harness.ClaudeAgentFakeOptions{
			McpCatalogYAML:       catalogYAML,
			AllowInline:          "",
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
		waitNodeSettledClaudeAgent(t, ep, nodeID, "fresh", "", 90*time.Second)
	})

	t.Run("signoff gate rejects unsigned bound output", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-signoff-missing",
			"scenario:signoff_missing",
			withSignoffGate(pubPEM, "endpoints", 1),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-signoff-missing")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", "agent/signoff_unobtained", 90*time.Second)
	})

	t.Run("inline mcp_servers refused when allow_inline=false", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-inline-refused",
			"scenario:signoff_ok",
			withInlineMcpServer("inline-bad", "http://example.invalid/mcp"),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-inline-refused")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", "agent/attribute_invalid", 90*time.Second)
	})

	t.Run("declared error class agent/rate_limited routes verbatim", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-rate-limited",
			"scenario:rate_limited",
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-rate-limited")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", "agent/rate_limited", 90*time.Second)
	})

	t.Run("env-var-referenced credential resolved at spawn but not persisted plaintext", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-env-ref-witness",
			"scenario:env_ref_witness",
			withSignoffGate(pubPEM, "cli_observation", 0),
			withCatalogMcpServerRef("validator"),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-env-ref-witness")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "fresh", "", 120*time.Second)

		// @story: claude-agent Acceptance clause (4)(a). The persisted
		bag := getLatestAttributesClaudeAgent(t, ep, nodeID)
		obs, ok := bag["cli_observation"].(map[string]any)
		if !ok {
			t.Fatalf("latest_attributes missing cli_observation: %v", bag)
		}
		gotDigest, _ := obs["validator_header_digest_sha256"].(string)
		wantDigest := sha256HexClaudeAgent(expectedAuthorizationHeader)
		if gotDigest != wantDigest {
			t.Fatalf("validator header digest mismatch: got %q, want %q (the executor's env-ref resolution didn't produce the expected plaintext header)",
				gotDigest, wantDigest)
		}

		// @story: claude-agent Acceptance clause (4)(b). The
		// plaintext token bytes must NOT appear anywhere in the persisted
		// attribute bag — neither as a header value, a stringified ref,
		// nor a leaked log line. Scan the serialized bag recursively; any
		// hit fails the clause.
		raw, err := json.Marshal(bag)
		if err != nil {
			t.Fatalf("re-marshal latest_attributes: %v", err)
		}
		if strings.Contains(string(raw), validatorPlaintextToken) {
			t.Fatalf("plaintext token %q leaked into rimsky-persisted node attributes: %s",
				validatorPlaintextToken, string(raw))
		}

		// @story: claude-agent Acceptance clause (4)(c). The
		// reference form in cli.mcp_servers persists as
		// `{ref: "validator"}` — the dispatch's cli.mcp_servers entry kept
		// its catalog reference, never resolved to inline.
		cli, _ := bag["cli"].(map[string]any)
		servers, _ := cli["mcp_servers"].([]any)
		if len(servers) != 1 {
			t.Fatalf("expected one cli.mcp_servers entry, got %d: %v", len(servers), servers)
		}
		first, _ := servers[0].(map[string]any)
		if got, _ := first["ref"].(string); got != "validator" {
			t.Fatalf("persisted cli.mcp_servers[0] missing ref=validator (got %v) — the dispatch attribute was rewritten with the resolved url/headers, leaking through a different surface",
				first)
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
			map[string]any{"name": name, "url": url},
		}
	}
}

func withCatalogMcpServerRef(refName string) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		cliDefault["mcp_servers"] = []any{
			map[string]any{"ref": refName},
		}
	}
}

func waitNodeSettledClaudeAgent(
	t *testing.T,
	ep harness.RimskyEndpoint,
	nodeID string,
	wantState, wantErrClass string,
	deadline time.Duration,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState    string
		lastErrClass string
		lastBody     string
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/nodes/"+nodeID, "")
		if status == http.StatusOK {
			var resp struct {
				State             string `json:"state"`
				CurrentErrorClass string `json:"current_error_class"`
			}
			lastBody = string(raw)
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.State
				lastErrClass = resp.CurrentErrorClass
				if wantState == "fresh" && resp.State == "fresh" {
					if hasWorkStartedEvent(t, ep, nodeID) {
						return
					}
				}
				if wantState == "failed" && resp.State == "failed" {
					if wantErrClass == "" || resp.CurrentErrorClass == wantErrClass {
						return
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %s did not settle to state=%q (err_class=%q) within %v; last_state=%q last_err_class=%q last_body=%s",
		nodeID, wantState, wantErrClass, deadline, lastState, lastErrClass, lastBody)
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
