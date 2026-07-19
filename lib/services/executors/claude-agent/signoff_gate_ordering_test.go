// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"fmt"
	"strings"
	"testing"
)

func TestRunAgentSignoffGate_SchemaCheckedBeforeSignoffGate(t *testing.T) {
	signer := makeTestSigner(t)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	maxSchema := 1
	opts.CliConfig = &CliConfig{
		MaxSchemaCorrections: &maxSchema,
		RequiredSignoffs:     []RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
	}
	opts.AttributesSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"endpoints": map[string]any{"type": "string"}},
	}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	badSchemaCall := fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":[{"url":"x"}]},"changed":true}`, token)

	result, _ := client.callTool("report_complete", badSchemaCall)
	firstText := client.firstText(result)
	if strings.Contains(firstText, "unmet sign-offs") {
		t.Fatalf("a schema-invalid submission (which also lacks required sign-offs) must be rejected "+
			"as a schema correction, not routed to the sign-off gate: %q", firstText)
	}
	if !strings.Contains(firstText, "correction 1/1") {
		t.Fatalf("expected the schema-correction counter to fire first, got %q", firstText)
	}

	result, _ = client.callTool("report_complete", badSchemaCall)
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("exhausted schema violation should be accepted-with-teardown, got %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeErrored || outcome.ErrorClass != "agent/schema_violation" {
		t.Fatalf("outcome = %+v, want errored/agent/schema_violation "+
			"(the schema gate must exhaust and resolve the run before the sign-off gate is ever consulted, "+
			"even though sign-offs were never satisfied)", outcome)
	}
}

func TestRunAgentSignoffGate_SignoffOnlyReachedAfterSchemaPasses(t *testing.T) {
	signer := makeTestSigner(t)
	runner := &fakeRunner{spawnHandles: []*fakeHandle{newFakeHandle(true)}}
	opts := baseRunOpts(runner)
	opts.CliConfig = &CliConfig{
		RequiredSignoffs: []RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
	}
	opts.AttributesSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"endpoints": map[string]any{"type": "string"}},
	}
	done := make(chan AgentOutcome, 1)
	go func() { done <- RunAgent(opts) }()

	req := runner.waitForSpawn(t)
	client, token := mcpClientFor(t, req)

	badSchemaCall := fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":[{"url":"x"}]},"changed":true}`, token)
	result, _ := client.callTool("report_complete", badSchemaCall)
	if !strings.Contains(client.firstText(result), "correction") {
		t.Fatalf("expected schema-correction rejection for the first, schema-invalid call, got %q", client.firstText(result))
	}

	goodSchemaNoSignoffCall := fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":"ok"},"changed":true}`, token)
	result, _ = client.callTool("report_complete", goodSchemaNoSignoffCall)
	if !strings.Contains(client.firstText(result), "unmet sign-offs") {
		t.Fatalf("a schema-valid submission that still lacks required sign-offs must reach the sign-off gate: %q", client.firstText(result))
	}

	sig := signer.sign(t, "disp-1", "ok")
	result, _ = client.callTool("report_complete",
		fmt.Sprintf(`{"token":%q,"attributes_delta":{"endpoints":"ok"},"changed":true,"signoffs":[%q]}`, token, sig))
	if client.firstText(result) != `{"status":"accepted"}` {
		t.Fatalf("expected acceptance once schema is valid and sign-off is satisfied, got %q", client.firstText(result))
	}

	outcome := <-done
	if outcome.Kind != OutcomeComplete {
		t.Fatalf("outcome = %+v, want OutcomeComplete", outcome)
	}
}
