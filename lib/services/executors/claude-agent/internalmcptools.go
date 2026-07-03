// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

const CallbackMCPServerName = "rimsky-callback"

var ParkReasons = []string{"await_callback", "snooze"}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

func toolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "report_complete",
			Description: "Report successful completion of this dispatch. Call exactly once at the end of the run. " +
				"`changed: true` if the work modified files; `changed: false` for no-op reports. " +
				"`attributes_delta` carries the terminal-final attribute writeback (omit when the run " +
				"made no attribute changes).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token", "changed"},
				"properties": map[string]any{
					"token":            map[string]any{"type": "string"},
					"attributes_delta": map[string]any{"type": "object"},
					"changed":          map[string]any{"type": "boolean"},
					"change_summary":   map[string]any{"type": []any{"string", "null"}},
					"signoffs": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
						"description": "base64 Ed25519 sign-off signatures, when the node " +
							"requires sign-offs",
					},
				},
			},
		},
		{
			Name:        "report_blocked",
			Description: "Report that work cannot continue (e.g. waiting on an external signal). Treated as agent_blocked.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token", "reason"},
				"properties": map[string]any{
					"token":   map[string]any{"type": "string"},
					"reason":  map[string]any{"type": "string"},
					"context": map[string]any{},
				},
			},
		},
		{
			Name:        "report_error",
			Description: "Report a terminal error. The dispatch is failed with the supplied error_class.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token", "error_class"},
				"properties": map[string]any{
					"token":       map[string]any{"type": "string"},
					"error_class": map[string]any{"type": "string"},
					"payload":     map[string]any{},
				},
			},
		},
		{
			Name: "report_park",
			Description: "Park the dispatch. The supervisor pauses the node until resume_at " +
				"elapses or an invalidate wakes it. ParkReason is the closed " +
				"two-value set (await_callback | snooze).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token", "reason"},
				"properties": map[string]any{
					"token": map[string]any{"type": "string"},
					"reason": map[string]any{
						"type":        "string",
						"enum":        []any{"await_callback", "snooze"},
						"description": "Typed park reason. The agent must pick one.",
					},
					"reason_note": map[string]any{
						"type":        "string",
						"description": "Optional human-readable annotation.",
					},
					"resume_at": map[string]any{
						"type":        "string",
						"format":      "date-time",
						"description": "Optional ISO 8601 timestamp at which to wake. Absent means signal-only.",
					},
				},
			},
		},
		{
			Name: "dispatch_context_read",
			Description: "Read the dispatch's identity and disposition as captured at executor " +
				"spawn. Returns dispatch_id and run_scope_id (always present as " +
				"strings, possibly empty in non-supervised invocations), and " +
				"prior_dispatch_id + prior_dispatch_disposition (one of " +
				"stale_recovery / retry_after_error / recalculate) when this dispatch " +
				"supersedes a predecessor. Returns the same snapshot for the " +
				"duration of the run.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token"},
				"properties": map[string]any{
					"token": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name: "attributes_read",
			Description: "Read the per-run attributes object as captured at executor spawn. " +
				"Returns the same snapshot for the duration of the run.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"token"},
				"properties": map[string]any{
					"token": map[string]any{"type": "string"},
				},
			},
		},
	}
}
