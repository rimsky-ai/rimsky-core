// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
)

const signoffPrivateKeyPath = "/etc/rimsky/fake-claude-signoff-private-key.pem"

const witnessPath = "/tmp/fake-claude-validator-header.txt"

func main() {
	argv := os.Args[1:]
	sessionID := readArg(argv, "--session-id")
	userPrompt := readArg(argv, "-p")
	resumeToken := readArg(argv, "--resume")
	mcpConfigPath := readArg(argv, "--mcp-config")

	callbackURL := os.Getenv("RIMSKY_CALLBACK_URL")
	callbackToken := os.Getenv("RIMSKY_CALLBACK_TOKEN")

	systemLine, _ := json.Marshal(map[string]any{
		"type":       "system",
		"subtype":    "fake-claude",
		"session_id": sessionID,
	})
	fmt.Println(string(systemLine))

	if callbackURL == "" {
		fail("RIMSKY_CALLBACK_URL is unset")
	}
	if callbackToken == "" {
		fail("RIMSKY_CALLBACK_TOKEN is unset")
	}
	if sessionID == "" && resumeToken == "" {
		fail("either --session-id (spawn) or --resume (resume) is required")
	}

	client := &mcpClient{url: callbackURL, token: callbackToken}
	client.initialize()

	switch {
	case strings.Contains(userPrompt, "scenario:signoff_ok"):
		scenarioSignoffOK(client, sessionID)
	case strings.Contains(userPrompt, "scenario:signoff_missing"):
		scenarioSignoffMissing(client)
	case strings.Contains(userPrompt, "scenario:rate_limited"):
		client.callTool("report_error", map[string]any{
			"error_class": "agent/rate_limited",
			"payload":     map[string]any{"reason": "fake-claude scripted upstream 429"},
		})
	case strings.Contains(userPrompt, "scenario:env_ref_witness"):
		scenarioEnvRefWitness(client, sessionID)
	case strings.Contains(userPrompt, "scenario:session_resume"):
		scenarioSessionResume(client, userPrompt, resumeToken)
	case strings.Contains(userPrompt, "scenario:dispatch_context_probe"):
		scenarioDispatchContextProbe(client)
	case strings.Contains(userPrompt, "scenario:per_node_witness"):
		scenarioPerNodeWitness(client, sessionID, mcpConfigPath, userPrompt)
	default:
		client.callTool("report_complete", map[string]any{
			"changed":          true,
			"attributes_delta": map[string]any{"stub": true},
		})
	}
}

func readArg(argv []string, name string) string {
	for i, a := range argv {
		if a == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func fail(msg string) {
	fmt.Fprintf(os.Stderr, "fake-claude: %s\n", msg)
	os.Exit(2)
}

type mcpClient struct {
	url       string
	token     string
	sessionID string
	nextID    int
}

func (c *mcpClient) request(method string, params map[string]any) map[string]any {
	c.nextID++
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  method,
		"params":  params,
	})
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		fail(fmt.Sprintf("MCP %s request build: %v", method, err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(fmt.Sprintf("MCP %s: %v", method, err))
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && c.sessionID == "" {
		c.sessionID = sid
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fail(fmt.Sprintf("MCP %s -> HTTP %d: %s", method, resp.StatusCode, raw))
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		fail(fmt.Sprintf("MCP %s response parse: %v: %s", method, err, raw))
	}
	if parsed["error"] != nil {
		errJSON, _ := json.Marshal(parsed["error"])
		fail(fmt.Sprintf("MCP %s error: %s", method, errJSON))
	}
	return parsed
}

func (c *mcpClient) initialize() {
	c.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "rimsky-fake-claude", "version": "1.0.0"},
	})
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (c *mcpClient) callTool(name string, args map[string]any) map[string]any {
	merged := map[string]any{"token": c.token}
	for k, v := range args {
		merged[k] = v
	}
	res := c.request("tools/call", map[string]any{
		"name":      name,
		"arguments": merged,
	})
	result, _ := res["result"].(map[string]any)
	return result
}

func loadSignoffKey() ed25519.PrivateKey {
	raw, err := os.ReadFile(signoffPrivateKeyPath)
	if err != nil {
		fail("signoff private key unavailable at " + signoffPrivateKeyPath + "; cannot sign")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		fail("signoff private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		fail("signoff private key parse: " + err.Error())
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		fail("signoff private key is not ed25519")
	}
	return key
}

func signValue(sessionID string, value any) string {
	key := loadSignoffKey()
	msg, err := claudeagent.BuildSignoffMessage(sessionID, value)
	if err != nil {
		fail("signoff message build: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, msg))
}

func scenarioSignoffOK(client *mcpClient, sessionID string) {
	endpoints := []any{map[string]any{"url": "https://verified.example/projectalpha/run1"}}
	sig := signValue(sessionID, endpoints)
	client.callTool("report_complete", map[string]any{
		"changed":          true,
		"attributes_delta": map[string]any{"endpoints": endpoints},
		"signoffs":         []any{sig},
	})
}

func scenarioSignoffMissing(client *mcpClient) {
	endpoints := []any{map[string]any{"url": "https://verified.example/projectalpha/run-bad"}}
	for attempt := 0; attempt < 10; attempt++ {
		client.callTool("report_complete", map[string]any{
			"changed":          true,
			"attributes_delta": map[string]any{"endpoints": endpoints},
		})
	}
}

func scenarioEnvRefWitness(client *mcpClient, sessionID string) {
	token := os.Getenv("VALIDATOR_TOKEN")
	if token == "" {
		fail("scenario:env_ref_witness: VALIDATOR_TOKEN not visible in the agent's own environment — " +
			"the executor's expose-env intersection did not pass the secret through to the CLI child")
	}
	resolvedHeader := "Bearer " + token
	sum := sha256.Sum256([]byte(resolvedHeader))
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(witnessPath, []byte(resolvedHeader), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "fake-claude: witness write failed: %v\n", err)
	}
	signedValue := map[string]any{
		"validator_header_digest_sha256": digest,
		"scenario":                       "env_ref_witness",
	}
	sig := signValue(sessionID, signedValue)
	client.callTool("report_complete", map[string]any{
		"changed": true,
		"attributes_delta": map[string]any{
			"cli_observation": map[string]any{
				"validator_header_digest_sha256": digest,
				"scenario":                       "env_ref_witness",
			},
		},
		"signoffs": []any{sig},
	})
}

var chainIDRe = regexp.MustCompile(`scenario:session_resume:([A-Za-z0-9_-]+)`)

func chainIDFromPrompt(prompt string) string {
	if m := chainIDRe.FindStringSubmatch(prompt); m != nil {
		return m[1]
	}
	return "default"
}

func sessionResumeLogPath(chainID string) string {
	return "/tmp/fake-claude-session-resume-" + chainID + ".json"
}

type sessionResumeState struct {
	Turn int    `json:"turn"`
	Name string `json:"name"`
}

func scenarioSessionResume(client *mcpClient, userPrompt string, resumeToken string) {
	chainID := chainIDFromPrompt(userPrompt)
	var turn int
	var priorRecall string
	if resumeToken == "" {
		turn = 1
		priorRecall = ""
		writeSessionResumeLog(chainID, sessionResumeState{Turn: 1, Name: "Alpha"})
	} else {
		raw, err := os.ReadFile(sessionResumeLogPath(chainID))
		var prior sessionResumeState
		if err != nil || json.Unmarshal(raw, &prior) != nil || prior.Turn == 0 {
			fail(fmt.Sprintf("scenario:session_resume: --resume %s but no prior conversation log at %s — "+
				"the executor passed --resume on what should have been a fresh dispatch",
				resumeToken, sessionResumeLogPath(chainID)))
		}
		turn = prior.Turn + 1
		priorRecall = prior.Name
		writeSessionResumeLog(chainID, sessionResumeState{Turn: turn, Name: priorRecall})
	}

	client.callTool("report_complete", map[string]any{
		"changed": true,
		"attributes_delta": map[string]any{
			"fake_cli_turn":         turn,
			"fake_cli_prior_recall": priorRecall,
			"fake_cli_resumed_with": resumeToken,
		},
	})
}

func writeSessionResumeLog(chainID string, state sessionResumeState) {
	raw, _ := json.Marshal(state)
	_ = os.WriteFile(sessionResumeLogPath(chainID), raw, 0o644)
}

var perNodeEnvVars = []string{"VALIDATOR_TOKEN", "REVIEWER_SEED"}

func scenarioPerNodeWitness(client *mcpClient, sessionID, mcpConfigPath, userPrompt string) {
	observed := map[string]any{
		"scenario":  "per_node_witness",
		"node_hint": readArg(strings.Fields(userPrompt), "node_hint"),
	}

	servers := []map[string]any{}
	if mcpConfigPath != "" {
		raw, err := os.ReadFile(mcpConfigPath)
		if err != nil {
			fail(fmt.Sprintf("per_node_witness: read mcp config %s: %v", mcpConfigPath, err))
		}
		var parsed struct {
			McpServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			fail(fmt.Sprintf("per_node_witness: parse mcp config: %v: %s", err, string(raw)))
		}
		for name, entry := range parsed.McpServers {
			transport, _ := entry["type"].(string)
			servers = append(servers, map[string]any{"name": name, "transport": transport})
		}
	}
	observed["mcp_servers"] = servers

	envDigests := map[string]string{}
	envPresence := map[string]bool{}
	for _, name := range perNodeEnvVars {
		val, ok := os.LookupEnv(name)
		envPresence[name] = ok
		if ok {
			sum := sha256.Sum256([]byte(val))
			envDigests[name] = hex.EncodeToString(sum[:])
		}
	}
	observed["env_presence"] = envPresence
	observed["env_digests"] = envDigests

	sig := signValue(sessionID, observed)
	client.callTool("report_complete", map[string]any{
		"changed": true,
		"attributes_delta": map[string]any{
			"cli_observation": observed,
		},
		"signoffs": []any{sig},
	})
}

func scenarioDispatchContextProbe(client *mcpClient) {
	result := client.callTool("dispatch_context_read", map[string]any{})
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		fail("dispatch_context_read returned no content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var observed map[string]any
	if err := json.Unmarshal([]byte(text), &observed); err != nil {
		fail("dispatch_context_read content parse: " + err.Error())
	}
	client.callTool("report_complete", map[string]any{
		"changed":          false,
		"attributes_delta": map[string]any{"dispatch_context": observed},
	})
}
