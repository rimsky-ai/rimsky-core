// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	_ "embed"
	"os"
	"strings"
)

// @concept: attribute
// @story: claude-agent-session-resume
// @decision: node-config-schema-format-go

//go:embed expected_attributes_schema.json
var expectedAttributesSchema []byte

const ExecutorName = "claude-agent"

const InProcURL = "inproc://claude-agent"

func SchemaBytes() []byte {
	out := make([]byte, len(expectedAttributesSchema))
	copy(out, expectedAttributesSchema)
	return out
}

// @concept: terminal-tag
func DeclaredTags() []string {
	raw := os.Getenv("RIMSKY_EXECUTOR_DECLARED_TAGS")
	if raw == "" {
		return nil
	}
	var tags []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			tags = append(tags, s)
		}
	}
	return tags
}

func DeclaredErrorClasses() []string {
	return []string{
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
}
