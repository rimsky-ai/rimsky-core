// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
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
