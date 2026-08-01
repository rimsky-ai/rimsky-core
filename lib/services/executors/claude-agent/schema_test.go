// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("claude-agent-schema.json", bytes.NewReader(SchemaBytes())); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("claude-agent-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateAttributes(t *testing.T, schema *jsonschema.Schema, doc string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	return schema.Validate(v)
}

func TestSchemaAcceptsInlineMcpServersAcrossThreeTransports(t *testing.T) {
	schema := compileSchema(t)
	doc := `{
		"user_prompt": "go",
		"cli": {
			"mcp_servers": [
				{"transport": "http", "name": "search", "url": "https://mcp.example.invalid/", "headers": {"authorization": "Bearer t"}, "allowed_tools": ["query"]},
				{"transport": "stdio", "name": "local-tool", "command": "/usr/local/bin/tool", "args": ["--serve"], "env": {"MODE": "quiet"}},
				{"transport": "module", "name": "loopback", "module": "./witness.js"},
				{"transport": "http-loopback", "name": "loopback-alias", "module": "./witness.js"}
			]
		}
	}`
	if err := validateAttributes(t, schema, doc); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestSchemaRejectsRetiredRefShape(t *testing.T) {
	schema := compileSchema(t)
	doc := `{"cli": {"mcp_servers": [{"ref": "catalog-entry"}]}}`
	if err := validateAttributes(t, schema, doc); err == nil {
		t.Fatal("expected the retired {ref} shape to be rejected")
	}
}

func TestSchemaRejectsTransportMissingItsParameters(t *testing.T) {
	schema := compileSchema(t)
	cases := map[string]string{
		"http without url":      `{"cli": {"mcp_servers": [{"transport": "http", "name": "x"}]}}`,
		"stdio without command": `{"cli": {"mcp_servers": [{"transport": "stdio", "name": "x"}]}}`,
		"module without module": `{"cli": {"mcp_servers": [{"transport": "module", "name": "x"}]}}`,
		"unknown transport":     `{"cli": {"mcp_servers": [{"transport": "carrier-pigeon", "name": "x", "url": "u"}]}}`,
		"missing name":          `{"cli": {"mcp_servers": [{"transport": "http", "url": "https://x/"}]}}`,
	}
	for name, doc := range cases {
		if err := validateAttributes(t, schema, doc); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestSchemaAcceptsExposeEnv(t *testing.T) {
	schema := compileSchema(t)
	doc := `{"cli": {"expose_env": ["VALIDATOR_TOKEN", "OTHER_VAR"]}}`
	if err := validateAttributes(t, schema, doc); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestSchemaRejectsEmptyExposeEnvName(t *testing.T) {
	schema := compileSchema(t)
	doc := `{"cli": {"expose_env": [""]}}`
	if err := validateAttributes(t, schema, doc); err == nil {
		t.Fatal("expected empty env-var name to be rejected")
	}
}

func TestDeclaredErrorClassesMatchesRetiredTypeScriptSet(t *testing.T) {
	want := []string{
		"agent/blocked",
		"agent/internal_error",
		"agent/attribute_invalid",
		"agent/schema_violation",
		"agent/cli_spawn_failed",
		"agent/timeout",
		"agent/tool_use_timeout",
		"agent/subprocess_exit/*",
		"agent/rate_limited",
		"agent/context_exceeded",
		"agent/tool_use_failed/*",
		"agent/refused",
		"agent/signoff_unobtained",
	}
	got := DeclaredErrorClasses()
	if len(got) != len(want) {
		t.Fatalf("DeclaredErrorClasses() = %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("DeclaredErrorClasses()[%d] = %q, want %q (full: got=%v want=%v)", i, got[i], w, got, want)
		}
	}
}

func TestDeclaredTagsParsesCommaSeparatedEnv(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_DECLARED_TAGS", " alpha , ,beta")
	tags := DeclaredTags()
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("tags = %v", tags)
	}
	t.Setenv("RIMSKY_EXECUTOR_DECLARED_TAGS", "")
	if DeclaredTags() != nil {
		t.Fatal("expected nil for empty env")
	}
}
