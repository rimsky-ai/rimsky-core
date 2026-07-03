// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	classes := DeclaredErrorClasses()
	if len(classes) != 13 {
		t.Fatalf("expected 13 declared error classes, got %d", len(classes))
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
