// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAttributeOverridesMatchOverlayRejection_OrdinalAndUnknownKeys(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-rejection", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{"type": "object"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	cases := []struct {
		name        string
		matcher     map[string]any
		errContains string
	}{
		{
			name:        "dispatch_index rejected with child_key redirect",
			matcher:     map[string]any{"dispatch_index": float64(2)},
			errContains: "dispatch_index",
		},
		{
			name:        "partition_index rejected",
			matcher:     map[string]any{"partition_index": float64(1)},
			errContains: "partition_index",
		},
		{
			name:        "nth_child rejected",
			matcher:     map[string]any{"nth_child": float64(0)},
			errContains: "nth_child",
		},
		{
			name:        "seq rejected",
			matcher:     map[string]any{"seq": float64(3)},
			errContains: "seq",
		},
		{
			name:        "unknown matcher key rejected",
			matcher:     map[string]any{"node_name": "worker"},
			errContains: "unknown matcher key",
		},
		{
			name: "non-primitive attrs.<path> value rejected",
			matcher: map[string]any{
				"attrs": map[string]any{
					"cli": map[string]any{"nested": "obj"},
				},
			},
			errContains: "must be a primitive",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"template":     tid,
				"instance_key": "ck-bm-rej-" + tt.name,
				"params":       map[string]any{},
				"target_agent": "scenario-default-agent",
				"attribute_overrides": map[string]any{
					"by_match": []any{
						map[string]any{
							"matcher": tt.matcher,
							"overlay": map[string]any{"x": 1},
						},
					},
				},
			})
			require.NoError(t, err)
			resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode,
				"want 400; got %d body=%s", resp.StatusCode, string(respBody))
			require.True(t, strings.Contains(string(respBody), tt.errContains),
				"response body %q must contain %q", string(respBody), tt.errContains)
		})
	}
}
