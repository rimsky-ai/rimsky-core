// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import "testing"

func TestClassifyAgentError(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{"empty stderr", "", ""},
		{"unclassified", "some ordinary failure", ""},
		{"context length exceeded", "error: context_length_exceeded", "agent/context_exceeded"},
		{"context window phrase", "the Context Window is full", "agent/context_exceeded"},
		{"prompt too long", "Prompt is too long for this model", "agent/context_exceeded"},
		{"tool use failed quoted name", `tool_use_failed: tool "WebSearch" exploded`, "agent/tool_use_failed/WebSearch"},
		{"tool use failed colon name", "tool_use_failed: Bash", "agent/tool_use_failed/Bash"},
		{"tool use failed bare name after rejected generic", "tool_use_failed: tool Grep returned garbage", "agent/tool_use_failed/Grep"},
		{"tool use failed unparseable", "tool_use_failed: !!!", "agent/tool_use_failed/unknown"},
		{"refusal marker", "the model answered (refusal)", "agent/refused"},
		{"refused by the model", "request was refused by the model", "agent/refused"},
		{"declined to respond", "the assistant declined to respond", "agent/refused"},
		{"bare refusal word", "classified as refusal by upstream", "agent/refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyAgentError(tc.stderr)
			if got != tc.want {
				t.Fatalf("ClassifyAgentError(%q) = %q, want %q", tc.stderr, got, tc.want)
			}
		})
	}
}
