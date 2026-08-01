// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParseCliConfig(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseCliConfig_RejectsNonStringPermissionMode(t *testing.T) {
	v := mustParseCliConfig(t, `{"permission_mode": 123}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a non-string permission_mode; silently coercing to '' defaults into the most permissive mode (bypassPermissions)")
	}
}

func TestParseCliConfig_RejectsNonStringDisallowedToolsEntry(t *testing.T) {
	v := mustParseCliConfig(t, `{"disallowed_tools": ["Bash", 1]}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a non-string disallowed_tools entry instead of silently dropping it")
	}
}

func TestParseCliConfig_RejectsNonArrayAllowedTools(t *testing.T) {
	v := mustParseCliConfig(t, `{"allowed_tools": "Read"}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection when allowed_tools is not an array")
	}
}

func TestParseCliConfig_RejectsNonIntegralMaxSchemaCorrections(t *testing.T) {
	v := mustParseCliConfig(t, `{"max_schema_corrections": 2.9}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a non-integral max_schema_corrections instead of silently truncating to 2")
	}
}

func TestParseCliConfig_RejectsNegativeMaxSignoffAttempts(t *testing.T) {
	v := mustParseCliConfig(t, `{"max_signoff_attempts": -1}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a negative max_signoff_attempts instead of silently falling back to the default")
	}
}

func TestParseCliConfig_RejectsNonStringMcpServerAllowedToolsEntry(t *testing.T) {
	v := mustParseCliConfig(t, `{"mcp_servers": [{"transport":"http","name":"x","url":"https://x/","allowed_tools":["a", 2]}]}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a non-string entry in an mcp server's allowed_tools")
	}
}

func TestParseCliConfig_RejectsNonStringMcpServerArgsEntry(t *testing.T) {
	v := mustParseCliConfig(t, `{"mcp_servers": [{"transport":"stdio","name":"x","command":"/bin/tool","args":["a", true]}]}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a non-string entry in an mcp server's args")
	}
}

func TestParseCliConfig_RejectsUnregisteredModuleTransport(t *testing.T) {
	v := mustParseCliConfig(t, `{"mcp_servers": [{"transport":"module","name":"x","module":"not-a-registered-module"}]}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a transport:module server whose specifier is not registered in this binary")
	}
	if !strings.Contains(err.Error(), "not-a-registered-module") {
		t.Fatalf("expected error to name the unregistered specifier, got %q", err.Error())
	}
}

func TestParseCliConfig_RejectsUnregisteredHTTPLoopbackTransport(t *testing.T) {
	v := mustParseCliConfig(t, `{"mcp_servers": [{"transport":"http-loopback","name":"x","module":"also-unregistered"}]}`)
	_, err := ParseCliConfig(v)
	if err == nil {
		t.Fatal("expected rejection of a transport:http-loopback server whose specifier is not registered in this binary")
	}
	if !strings.Contains(err.Error(), "also-unregistered") {
		t.Fatalf("expected error to name the unregistered specifier, got %q", err.Error())
	}
}

func TestParseCliConfig_AcceptsRegisteredModuleTransport(t *testing.T) {
	registerTestModule(t, "test-parse-registered", func() *ModuleMcpServer {
		return &ModuleMcpServer{Name: "registered-server"}
	})
	v := mustParseCliConfig(t, `{"mcp_servers": [{"transport":"module","name":"x","module":"test-parse-registered"}]}`)
	if _, err := ParseCliConfig(v); err != nil {
		t.Fatalf("expected a registered module specifier to parse cleanly, got %v", err)
	}
}
