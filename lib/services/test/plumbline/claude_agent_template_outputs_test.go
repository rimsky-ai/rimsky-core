// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: expected-attributes-schema-closed
// @concept: executor
// @concept: attribute

package plumbline

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
)

func claudeAgentHooks() node.RegistryHooks {
	return node.RegistryHooks{
		ExecutorExpectedAttributesSchema: func(string) ([]byte, bool) {
			return claudeagent.SchemaBytes(), true
		},
	}
}

func sessionResumeWorker(extra map[string]any) node.TemplateNodeDef {
	properties := map[string]any{
		"model":         map[string]any{"type": "string", "default": "claude-sonnet-4-5"},
		"system_prompt": map[string]any{"type": "string", "default": "session continuity"},
		"user_prompt":   map[string]any{"type": "string", "default": "..."},
		"cli":           map[string]any{"type": "object", "default": map[string]any{}},
	}
	for name, prop := range extra {
		properties[name] = prop
	}
	return node.TemplateNodeDef{
		Type:     "worker",
		Executor: "claude-agent",
		Subscribes: []node.SubscriptionEntry{{
			Node:                 "worker",
			Type:                 "terminal/success",
			When:                 "payload.attributes_delta.turn < 3",
			ForceUpstreamRefresh: node.BoolPtr(false),
		}},
		Attributes: &node.NodeAttributesDef{Schema: map[string]any{
			"type":       "object",
			"properties": properties,
		}},
	}
}

// @decision: expected-attributes-schema-closed
func TestSessionResumeTemplateRegistersWithItsAgentOutputsDeclared(t *testing.T) {
	spec := &node.TemplateSpec{
		Name:    "session-resume-demo",
		Version: "1",
		Nodes: []node.TemplateNodeDef{sessionResumeWorker(map[string]any{
			"turn":   map[string]any{"type": "integer", "readOnly": true},
			"recall": map[string]any{"type": "string", "readOnly": true},
		})},
	}

	res := node.ValidateTemplate(spec, claudeAgentHooks())
	if !res.Ok() {
		t.Fatalf("the session-resume example must register: claude-agent's writeback bag is author-defined, so a "+
			"template names the outputs it reads back; errors = %+v", res.Errors)
	}
}

// @decision: expected-attributes-schema-closed
func TestClaudeAgentTemplateStillRefusesAMisspeltInput(t *testing.T) {
	spec := &node.TemplateSpec{
		Name:    "session-resume-demo",
		Version: "1",
		Nodes: []node.TemplateNodeDef{sessionResumeWorker(map[string]any{
			"user_promt": map[string]any{"type": "string", "default": "..."},
		})},
	}

	res := node.ValidateTemplate(spec, claudeAgentHooks())
	if res.Ok() {
		t.Fatal("an input the executor's schema does not declare is a misspelling, and registration refuses it " +
			"whether or not the executor leaves its outputs open")
	}
}

// @decision: expected-attributes-schema-closed
func TestClaudeAgentTemplateRefusesAnUndeclaredWritableProperty(t *testing.T) {
	spec := &node.TemplateSpec{
		Name:    "session-resume-demo",
		Version: "1",
		Nodes: []node.TemplateNodeDef{sessionResumeWorker(map[string]any{
			"turn": map[string]any{"type": "integer", "default": 0},
		})},
	}

	res := node.ValidateTemplate(spec, claudeAgentHooks())
	if res.Ok() {
		t.Fatal("an executor that leaves its outputs open admits an undeclared property the template marks " +
			"readOnly, and nothing else")
	}
}
