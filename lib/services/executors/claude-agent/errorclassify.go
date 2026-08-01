// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"regexp"
	"strings"
)

func ClassifyAgentError(stderr string) string {
	if stderr == "" {
		return ""
	}
	lower := strings.ToLower(stderr)

	if strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "context_window") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "prompt is too long") {
		return "agent/context_exceeded"
	}

	if strings.Contains(lower, "tool_use_failed") || strings.Contains(lower, "tool execution failed") {
		tool := parseToolName(stderr)
		if tool == "" {
			tool = "unknown"
		}
		return "agent/tool_use_failed/" + tool
	}

	if strings.Contains(lower, "(refusal)") ||
		strings.Contains(lower, "refused by the model") ||
		strings.Contains(lower, "declined to respond") ||
		(refusalWordRe.MatchString(lower) && !refusalNegatedRe.MatchString(lower)) {
		return "agent/refused"
	}

	return ""
}

var (
	refusalWordRe    = regexp.MustCompile(`\brefusal\b`)
	refusalNegatedRe = regexp.MustCompile(`(?i)\b(?:no|not|non|isn'?t|wasn'?t|without)\b(?:\s+\S+){0,3}\s+refusal\b`)
	toolQuotedRe     = regexp.MustCompile("(?i)tool\\s+[\"'`]([^\"'`]+)[\"'`]")
	toolColonRe      = regexp.MustCompile("(?i)tool_use_failed[:\\s]+[\"'`]?([A-Za-z0-9_./-]+)[\"'`]?")
	toolBareRe       = regexp.MustCompile(`(?i)\btool\s+([A-Za-z0-9_./-]+)\s+(?:returned|failed|errored)`)
)

func parseToolName(stderr string) string {
	if m := toolQuotedRe.FindStringSubmatch(stderr); m != nil && m[1] != "" {
		return m[1]
	}
	if m := toolColonRe.FindStringSubmatch(stderr); m != nil && m[1] != "" && strings.ToLower(m[1]) != "tool" {
		return m[1]
	}
	if m := toolBareRe.FindStringSubmatch(stderr); m != nil && m[1] != "" {
		return m[1]
	}
	return ""
}
