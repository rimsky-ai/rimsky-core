// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestValidateAttributeOverrides(t *testing.T) {
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
			// @constraint: issue #4 coverage: a null fragment value is structurally
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
			// @constraint: issue #3 coverage: executor declared in rimsky.yml but not
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
			// @constraint: JSON `null` at the top-level for `by_executor` decodes to
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
			err := validateAttributeOverrides(tt.input, nodes, nil, executors)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.errContains)
				}
				if !errors.Is(err, errAttributeOverridesInvalid) {
					t.Fatalf("error not wrapped with errAttributeOverridesInvalid: %v", err)
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

// TestValidateAttributeOverrides_ByMatch covers the by_match matcher-
// overlay grammar added per
func TestValidateAttributeOverrides_ByMatch(t *testing.T) {
	executors := map[string]ExecutorEntry{
		"claude-agent": {Transport: "grpc", Endpoint: "claude-agent:9090"},
		"http-node":    {Transport: "grpc", Endpoint: "http-node:9090"},
	}
	nodes := []nodepkg.TemplateNodeDef{
		{Type: "fan", Executor: "claude-agent"},
		{Type: "fan-child", Executor: "claude-agent"},
		{Type: "merge", Executor: "claude-agent"},
	}
	withGraphs := []spec.GraphSpec{
		{Name: spec.MainGraphName, Nodes: []nodepkg.TemplateNodeDef{
			{Type: "fan", Executor: "claude-agent"},
			{Type: "merge", Executor: "claude-agent"},
		}},
		{Name: "worker", Entry: "fan-child", Exit: "fan-child", Nodes: []nodepkg.TemplateNodeDef{
			{Type: "fan-child", Executor: "claude-agent"},
		}},
	}

	tests := []struct {
		name        string
		graphs      []spec.GraphSpec
		input       map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name: "valid by_match: single entry with node_type + child_key",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{
							"node_type": "fan-child",
							"child_key": "k1",
						},
						"overlay": map[string]any{"tag": "for-k1"},
					},
				},
			},
		},
		{
			name: "valid by_match: empty matcher {} accepted",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{},
						"overlay": map[string]any{"flag": true},
					},
				},
			},
		},
		{
			name: "valid by_match: empty list accepted",
			input: map[string]any{
				"by_match": []any{},
			},
		},
		{
			name: "by_match not an array",
			input: map[string]any{
				"by_match": map[string]any{"oops": true},
			},
			wantErr:     true,
			errContains: "by_match must be an array",
		},
		{
			name: "by_match entry has extra top-level key",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"node_type": "fan"},
						"overlay": map[string]any{"x": 1},
						"notes":   "this should be rejected",
					},
				},
			},
			wantErr:     true,
			errContains: "unknown entry key",
		},
		{
			name: "by_match entry missing matcher (implicit {} accepted)",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"overlay": map[string]any{"x": 1},
					},
				},
			},
		},
		{
			// @constraint: symmetry with the missing-matcher case above. A JSON
			// producer may serialise a nil matcher either way; both
			// shapes are equivalent to the runtime evaluator
			// (`len(matcher) == 0` → wildcard) and the validator must
			// agree.
			name: "by_match entry explicit matcher: null (treated as wildcard)",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": nil,
						"overlay": map[string]any{"x": 1},
					},
				},
			},
		},
		{
			// @constraint: non-object, non-null matcher remains a hard reject. A
			// JSON array or scalar matcher is a typo, not a wildcard
			// — the loud-rejection vocabulary stays.
			name: "by_match entry matcher: array still rejected",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": []any{"not", "an", "object"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "matcher must be an object",
		},
		{
			name: "by_match entry missing overlay",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"node_type": "fan"},
					},
				},
			},
			wantErr:     true,
			errContains: "overlay is required and must be an object",
		},
		{
			name: "unknown matcher key (e.g. node_name)",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"node_name": "fan"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "unknown matcher key",
		},
		{
			name: "ordinal-shaped matcher key dispatch_index",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"dispatch_index": float64(2)},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "dispatch_index",
		},
		{
			name: "ordinal-shaped matcher key nth_child",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"nth_child": float64(0)},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "nth_child",
		},
		{
			name: "ordinal-shaped matcher key partition_index",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"partition_index": float64(1)},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "partition_index",
		},
		{
			name: "ordinal-shaped matcher key seq",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"seq": float64(3)},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "seq",
		},
		{
			name: "matcher.node_type references unknown node",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"node_type": "made-up"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "unknown node",
		},
		{
			name: "matcher.executor references unknown executor",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"executor": "made-up-executor"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "unknown executor name",
		},
		{
			name: "matcher.executor references declared-but-unused executor",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"executor": "http-node"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "executor not referenced by any template node",
		},
		{
			name: "matcher.graph \"main\" accepted on flat-Nodes template",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"graph": "main"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
		},
		{
			name: "matcher.graph other rejected on flat-Nodes template",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"graph": "worker"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "no declared sub-graphs",
		},
		{
			name:   "matcher.graph declared sub-graph accepted",
			graphs: withGraphs,
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"graph": "worker"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
		},
		{
			name:   "matcher.graph unknown name rejected with sub-graphs",
			graphs: withGraphs,
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"graph": "nope"},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "unknown graph",
		},
		{
			name: "matcher.attrs with primitive values",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{
							"attrs": map[string]any{
								"cli.model":     "gpt",
								"cli.iter":      float64(3),
								"cli.allow_old": true,
							},
						},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
		},
		{
			// @constraint: spec: empty-string child_key is the non-fan-out sentinel,
			// not a matcher target. Accepting it would silently fire on
			// every non-fan-out dispatch, contradicting the spec's
			// "matchers specifying child_key won't apply to them" rule.
			name: "matcher.child_key empty string rejected",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"child_key": ""},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "non-empty string",
		},
		{
			name: "matcher.child_key non-string rejected",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{"child_key": float64(7)},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "non-empty string",
		},
		{
			name: "matcher.attrs with non-primitive value (object)",
			input: map[string]any{
				"by_match": []any{
					map[string]any{
						"matcher": map[string]any{
							"attrs": map[string]any{
								"cli": map[string]any{"model": "gpt"},
							},
						},
						"overlay": map[string]any{"x": 1},
					},
				},
			},
			wantErr:     true,
			errContains: "must be a primitive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAttributeOverrides(tt.input, nodes, tt.graphs, executors)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.errContains)
				}
				if !errors.Is(err, errAttributeOverridesInvalid) {
					t.Fatalf("error not wrapped with errAttributeOverridesInvalid: %v", err)
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
