// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

var forbiddenRedirectWords = []string{"renamed", "instead", "no longer", "deprecated"}

type retiredTemplateKeyCase struct {
	name      string
	spec      map[string]any
	wantToken string
}

var retiredTemplateKeyCases = []retiredTemplateKeyCase{
	{
		name: "top-level frame_timeout_ms",
		spec: map[string]any{
			"name":             "retired-frame-timeout-key-fixture",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []any{
				map[string]any{"type": "worker", "executor": "stub"},
			},
		},
		wantToken: "frame_timeout_ms",
	},
	{
		name: "fan_out.error_policy.cancel_siblings",
		spec: map[string]any{
			"name":    "retired-cancel-siblings-key-fixture",
			"version": "1",
			"nodes": []any{
				map[string]any{
					"type":     "fan",
					"executor": "stub",
					"claim_producers": []any{
						map[string]any{"name": "content", "alias": "items", "intent": "r", "selector": "{{params.s}}"},
					},
					"fan_out": map[string]any{
						"claim":             "items",
						"partition_request": `{"list":[{"key":"a"}]}`,
						"error_policy": map[string]any{
							"kind":            "strict",
							"cancel_siblings": true,
						},
					},
				},
			},
		},
		wantToken: "cancel_siblings",
	},
	{
		name: "top-level templates key (config-block name reused as a template-spec key)",
		spec: map[string]any{
			"name":    "retired-templates-block-key-fixture",
			"version": "1",
			"templates": map[string]any{
				"ref_validation_mode": "none",
			},
			"nodes": []any{
				map[string]any{"type": "worker", "executor": "stub"},
			},
		},
		wantToken: "templates",
	},
	{
		name: "top-level max_park_duration",
		spec: map[string]any{
			"name":              "retired-max-park-duration-key-fixture",
			"version":           "1",
			"max_park_duration": "1h",
			"nodes": []any{
				map[string]any{"type": "worker", "executor": "stub"},
			},
		},
		wantToken: "max_park_duration",
	},
}

func TestRegistrationRejectsRetiredTemplateKeys(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	for _, tc := range retiredTemplateKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, h.ControlBase+"/v1/templates", map[string]any{"spec": tc.spec})
			require.Equal(t, http.StatusBadRequest, resp.status,
				"registration must reject a body carrying the retired %s key: %s", tc.wantToken, resp.bodyStr())

			errMsg := resp.stringField("error")
			require.Contains(t, errMsg, tc.wantToken,
				"rejection must name the offending key: %s", resp.bodyStr())
			require.Contains(t, strings.ToLower(errMsg), "unknown field",
				"%s must fail through the same generic unknown-field path as any other "+
					"undeclared key — no dedicated redirect or migration guidance for this "+
					"retired key: %s", tc.wantToken, resp.bodyStr())

			lower := strings.ToLower(errMsg)
			for _, word := range forbiddenRedirectWords {
				require.NotContains(t, lower, word,
					"pure erasure: rejection for %s must not direct the caller toward a replacement setting: %s",
					tc.wantToken, resp.bodyStr())
			}
		})
	}
}
