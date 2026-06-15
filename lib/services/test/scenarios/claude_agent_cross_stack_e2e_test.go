// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cross-stack proof for STORY-claude-agent: an operator wiring an agentic
// node sees the claude-agent executor's four contract clauses honored
// end-to-end through the REAL assembled product — rimsky stack on one
// side, real claude-agent gRPC executor on the other, with the CLI runner
// path actually exercised. The third-party Claude binary is impractical
// in CI (credentials + cost), so the executor's CLI runner is bound to a
// stub script that mimics the Claude CLI's wire shape (--session-id,
// --mcp-config, -p, RIMSKY_CALLBACK_URL/TOKEN env). The stub is the only
// non-real component: every other path the dispatch walks — the gRPC
// async-handoff handshake, the internal MCP callback server, the
// signoff gate, the env-ref resolution at spawn, the attributes_set
// writeback, the final AsyncCallbackBody → supervisor settle — is real.
//
// The four Acceptance clauses (and matching Falsifier failure modes) are
// each driven by one dispatch:
//
//  1. **Sign-off gate accepts real bound output** — a template wires
//     `cli.required_signoffs` against a real Ed25519 public key; the stub
//     CLI's "scenario:signoff_ok" branch produces a real signature over
//     the bound writeback and calls report_complete with that signature
//     in `signoffs`. The dispatch must settle SUCCESS. A second template,
//     identical except the stub omits the signature, must settle FAILED
//     with terminal error_class = "agent/signoff_unobtained" —
//     proving the gate rejects empty-output signatures. Falsifier:
//     "the sign-off accepts a signature over stale output (bound to null
//     when output was emitted incrementally)".
//
//  2. **MCP catalog refuses inline when allow_inline=false** — a third
//     template declares an INLINE `cli.mcp_servers: [{name,url}]` while
//     the executor runs with the executor-wide default
//     `allow_inline=false`. The dispatch must terminate with error_class
//     "agent/attribute_invalid" — the executor's resolveHostServers
//     rejects the inline entry at the parseCliConfig boundary, before
//     spawning the CLI. Falsifier: "allow_inline=false is silently
//     accepted alongside an inline server definition".
//
//  3. **Declared error classes route via policy** — a fourth template
//     wires user_prompt "scenario:rate_limited"; the stub CLI calls
//     report_error with error_class "agent/rate_limited". The
//     supervisor's settled node row carries current_error_class equal to
//     that exact declared class — not a generic agent/internal_error or
//     a fall-through. Falsifier: "a declared error class fires but the
//     policy router treats it as generic".
//
//  4. **Env-var-referenced credentials don't persist in plaintext** — a
//     fifth template wires `cli.mcp_servers: [{ref: "validator"}]`
//     against a catalog the executor loads at startup whose `validator`
//     entry has `headers: {Authorization: "Bearer ${env:VALIDATOR_TOKEN}"}`.
//     The executor's spawn-time resolveHeaderEnvRefs inlines the plaintext
//     into the CLI's --mcp-config; the stub CLI reads it, records its
//     SHA-256 digest via attributes_set (proving the resolution reached
//     the spawn), then calls report_complete. The rimsky-side persisted
//     attribute bag on the worker node must:
//       (a) contain the SHA-256 digest (the witness proves the resolution
//           happened), AND
//       (b) NEVER contain the plaintext token bytes anywhere in the
//           recursive payload, AND
//       (c) still carry the `${env:VALIDATOR_TOKEN}` reference form in
//           the cli.mcp_servers structure as it was at registration
//           time.
//     Falsifier: "an env-var-referenced credential persists in plaintext
//     attributes".

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

// validatorPlaintextToken is the token the fake claude-agent's
// VALIDATOR_TOKEN env carries. Chosen as a stable byte sequence with no
// natural occurrence — if the rimsky-side persistence ever leaks the
// plaintext into a stored attribute, the `strings.Contains` scan will
// find this exact string and fail clause (4)(b).
const validatorPlaintextToken = "validator-plaintext-token-do-not-leak-9e7f7c2a"

// expectedAuthorizationHeader is the plaintext shape the executor's
// spawn-time env-ref resolution must produce in the CLI's --mcp-config
// `validator` server entry. The stub witnesses this string and digests
// it; the digest is what should land in rimsky-persisted attributes.
const expectedAuthorizationHeader = "Bearer " + validatorPlaintextToken

// TestClaudeAgentCrossStack drives all four STORY-claude-agent
// Acceptance clauses end-to-end through the real assembled product
// against the fake-claude-binary executor. Each clause is one dispatch
// against the same running rimsky stack.
func TestClaudeAgentCrossStack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @deliberate: fresh keypair per test run so the template's public key
	// and the executor container's private key are paired without
	// cross-test leakage.
	pubPEM, privPEM := mustGenerateEd25519PEMs(t)

	// @constraint: catalog YAML the executor loads at startup. The
	// `validator` entry's Authorization header carries a
	// ${env:VALIDATOR_TOKEN} ref; the executor resolves it at spawn
	// (env-refs.ts), so the stub CLI witnesses the resolved plaintext in
	// its --mcp-config.
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
			AllowInline:          "", // @deliberate: empty → default false → inline rejected
			SignoffPrivateKeyPEM: privPEM,
			ExtraEnv: map[string]string{
				"VALIDATOR_TOKEN": validatorPlaintextToken,
			},
		},
	)

	// @deliberate: Postgres backend rather than the SQLite default. This
	// scenario drives FIVE sequential deploy → instance → dispatch
	// round-trips against the same rimsky stack, and the SQLite
	// single-writer path has shown non-deterministic dispatch latency on
	// multi-instance sequences (see verifier_severity_partition_e2e_test.go
	// which made the same switch for the same reason). The contract under
	// test — the four STORY-claude-agent Acceptance clauses — is
	// persistence-backend-agnostic. The single-node SQLite loop is covered
	// by sqlite_all_in_one_test.go.
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
			// @deliberate: max_signoff_attempts=1 keeps the retry budget
			// short so the gate's rejection lands quickly. The stub's retry
			// loop sees a non-rejected status only after the budget is
			// exhausted.
			withSignoffGate(pubPEM, "endpoints", 1),
		))
		iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-signoff-missing")
		nodeID := resolveWorkerNodeID(t, ep, iid, "worker")
		waitNodeSettledClaudeAgent(t, ep, nodeID, "failed", "agent/signoff_unobtained", 90*time.Second)
	})

	t.Run("inline mcp_servers refused when allow_inline=false", func(t *testing.T) {
		tid := deployScenarioTemplate(t, ep, buildClaudeAgentTemplate(
			"claude-agent-inline-refused",
			// @deliberate: any prompt — the dispatch never spawns the CLI
			// because the inline rejection fires at parse/resolve time
			// inside the executor, before the CLI runner is invoked.
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
		// attribute bag carries the SHA-256 digest of the resolved
		// Authorization header — proving the executor's spawn-time env-ref
		// resolution did populate the CLI's --mcp-config (otherwise the
		// stub CLI's witness write would have failed loud and the dispatch
		// would have settled with an error).
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

// claudeAgentTemplateOption is a tiny mutator over a template's worker-
// node attribute schema. Each clause's template differs only in the cli
// gate / mcp_servers wiring, so the per-option mutator keeps the test
// readable.
type claudeAgentTemplateOption func(workerNodeAttrs map[string]any)

// buildClaudeAgentTemplate returns the `POST /templates` body for a
// single-node template wiring the claude-agent executor with a fixed
// user_prompt (the stub CLI branches on prompt contents) and any opt
// modifications to the worker node's attribute schema.
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
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
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

// withSignoffGate layers cli.required_signoffs onto the worker node's
// attribute defaults. The gate pins `path` so the signature must cover
// the value at that path; `maxAttempts` rides max_signoff_attempts.
// maxAttempts=0 leaves the executor's default in place.
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

// withInlineMcpServer layers an INLINE cli.mcp_servers entry (the
// {name,url} form) so the dispatch trips the executor's allow_inline=
// false policy at parse time.
func withInlineMcpServer(name, url string) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		cliDefault["mcp_servers"] = []any{
			map[string]any{"name": name, "url": url},
		}
	}
}

// withCatalogMcpServerRef layers a `{ref: <name>}` cli.mcp_servers entry
// referencing a catalog entry the executor loaded at startup.
func withCatalogMcpServerRef(refName string) claudeAgentTemplateOption {
	return func(attrs map[string]any) {
		cli := attrs["schema"].(map[string]any)["properties"].(map[string]any)["cli"].(map[string]any)
		cliDefault := cli["default"].(map[string]any)
		cliDefault["mcp_servers"] = []any{
			map[string]any{"ref": refName},
		}
	}
}

// waitNodeSettledClaudeAgent polls GET /v1/nodes/{id} until the node
// reaches `wantState`; when wantErrClass is non-empty, also asserts the
// node's current_error_class is exactly that string. A timeout fatals
// the test.
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
				// @constraint: `fresh` is also the freshly-created state,
				// so a sole `fresh` doesn't prove a dispatch settled.
				// Cross-check the observability node-events feed to confirm
				// a real work_started fired before claiming success.
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

// hasWorkStartedEvent checks the observability node-events feed for a
// `work_started` event — the unambiguous proof a real dispatch took
// place. Returns true iff the event is present (any state).
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

// getLatestAttributesClaudeAgent fetches GET /v1/nodes/{id} and returns
// the parsed latest_attributes bag (the forensic last-attribute snapshot
// rimsky persists for the node).
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

// sha256HexClaudeAgent matches the digest the stub CLI writes in
// fake-claude.js's scenario:env_ref_witness branch.
func sha256HexClaudeAgent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// mustGenerateEd25519PEMs generates a fresh Ed25519 keypair and returns
// (publicSPKIPEM, privatePKCS8PEM). Matches the PEM shapes
// signoff.ts and signoff-test-signer.ts produce in the TS executor
// tests, so the executor's verify path and the stub CLI's sign path
// agree on the wire bytes.
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
