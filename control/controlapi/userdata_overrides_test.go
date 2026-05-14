// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"strings"
	"testing"

	nodepkg "github.com/fallguy/rimsky/graph/node"
)

func TestValidateUserdataOverrides(t *testing.T) {
	executors := map[string]ExecutorEntry{
		"claude-agent": {Transport: "grpc", Endpoint: "claude-agent:9090"},
		"http-node":    {Transport: "grpc", Endpoint: "http-node:9090"},
	}
	nodes := []nodepkg.TemplateNodeDef{
		{Type: "reference-pass", Executor: "claude-agent"},
		{Type: "area-pass", Executor: "claude-agent"},
		{Type: "consolidate", Executor: "claude-agent"},
	}

	tests := []struct {
		name        string
		input       map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name:  "empty overrides → nil",
			input: nil,
		},
		{
			name:  "empty map → nil",
			input: map[string]any{},
		},
		{
			name: "valid by_executor + by_node",
			input: map[string]any{
				"by_executor": map[string]any{
					"claude-agent": map[string]any{"cli": map[string]any{"x": 1}},
				},
				"by_node": map[string]any{
					"area-pass": map[string]any{"cli": map[string]any{"y": 2}},
				},
			},
		},
		{
			name: "unknown top-level key rejected",
			input: map[string]any{
				"global": map[string]any{"cli": "x"},
			},
			wantErr:     true,
			errContains: "unknown top-level key",
		},
		{
			name: "by_executor not an object",
			input: map[string]any{
				"by_executor": "not-a-map",
			},
			wantErr:     true,
			errContains: "by_executor must be an object",
		},
		{
			name: "unknown executor name",
			input: map[string]any{
				"by_executor": map[string]any{
					"made-up-executor": map[string]any{"cli": "x"},
				},
			},
			wantErr:     true,
			errContains: "unknown executor name",
		},
		{
			name: "executor entry not an object",
			input: map[string]any{
				"by_executor": map[string]any{
					"claude-agent": "scalar-not-allowed",
				},
			},
			wantErr:     true,
			errContains: "by_executor entry must be an object",
		},
		{
			name: "by_node not an object",
			input: map[string]any{
				"by_node": "not-a-map",
			},
			wantErr:     true,
			errContains: "by_node must be an object",
		},
		{
			name: "unknown node name",
			input: map[string]any{
				"by_node": map[string]any{
					"made-up-node": map[string]any{"cli": "x"},
				},
			},
			wantErr:     true,
			errContains: "unknown node name",
		},
		{
			name: "node entry not an object",
			input: map[string]any{
				"by_node": map[string]any{
					"area-pass": "scalar-not-allowed",
				},
			},
			wantErr:     true,
			errContains: "by_node entry must be an object",
		},
		{
			// Issue #4 coverage: a null fragment value is structurally
			// not an object; reject with "must be an object" rather than
			// flowing through to declared/used checks.
			name: "by_executor entry is null",
			input: map[string]any{
				"by_executor": map[string]any{
					"claude-agent": nil,
				},
			},
			wantErr:     true,
			errContains: "by_executor entry must be an object",
		},
		{
			name: "by_node entry is null",
			input: map[string]any{
				"by_node": map[string]any{
					"area-pass": nil,
				},
			},
			wantErr:     true,
			errContains: "by_node entry must be an object",
		},
		{
			// Issue #3 coverage: executor declared in rimsky.yml but not
			// referenced by any template node is rejected — overrides
			// targeting it would silently no-op at dispatch.
			name: "by_executor: declared executor not referenced by template",
			input: map[string]any{
				"by_executor": map[string]any{
					"http-node": map[string]any{"cli": "x"},
				},
			},
			wantErr:     true,
			errContains: "executor not referenced by any template node",
		},
		{
			// JSON `null` at the top-level for `by_executor` decodes to
			// untyped nil; the type assertion `raw.(map[string]any)`
			// fails so the validator must reject with the standard
			// "must be an object" message rather than panicking or
			// flowing through to the inner-entry checks.
			name: "by_executor at top-level is null",
			input: map[string]any{
				"by_executor": nil,
			},
			wantErr:     true,
			errContains: "by_executor must be an object",
		},
		{
			name: "by_node at top-level is null",
			input: map[string]any{
				"by_node": nil,
			},
			wantErr:     true,
			errContains: "by_node must be an object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUserdataOverrides(tt.input, nodes, executors)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.errContains)
				}
				if !errors.Is(err, errUserdataOverridesInvalid) {
					t.Fatalf("error not wrapped with errUserdataOverridesInvalid: %v", err)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
