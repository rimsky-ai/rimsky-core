// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stores

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const selectorScopeA = "/scope-A"

func TestAcceptance_ClaimScopeEndToEnd(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: syncCaps,
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(claimScopeTemplate("claim-scope-e2e", "{{claim.a.claim_scope}}"))

	iid := h.CreateInstance(tid, "ck-claim-scope-e2e", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	var region any
	var sawWorkerDispatch bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, obs := range h.Stub.Observed() {
			if obs.NodeType != "worker" {
				continue
			}
			sawWorkerDispatch = true
			if v, ok := obs.Attributes["region"]; ok {
				region = v
			}
		}
		if sawWorkerDispatch && region != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawWorkerDispatch, "expected the worker to be dispatched to the real executor")
	require.Equal(t, selectorScopeA, region,
		"executor must receive region resolved to the live claim's claim_scope (the producer's returned ClaimScope, stringified)")

	body, err := json.Marshal(map[string]any{
		"spec": claimScopeTemplateJSON("claim-scope-e2e-legacy", "{{claim.a.scope}}"),
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/templates", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"legacy {{claim.a.scope}} spelling must be rejected at registration with 400; got %d body=%s",
		resp.StatusCode, string(respBody))
	require.True(t, strings.Contains(string(respBody), "claim_scope"),
		"the legacy-spelling rejection must name the canonical claim_scope segment; body=%s", string(respBody))
}

func claimScopeTemplate(name, directive string) node.TemplateSpec {
	return node.TemplateSpec{
		Name: name, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("content", selectorScopeA, "rw", "a")),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":   "string",
							"source": directive,
						},
					},
				}),
			),
		},
	}
}

func claimScopeTemplateJSON(name, directive string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "1",
		"nodes": []any{
			map[string]any{
				"type":     "worker",
				"executor": "stub",
				"claim_producers": []any{
					map[string]any{
						"name":     "content",
						"selector": selectorScopeA,
						"intent":   "rw",
						"alias":    "a",
					},
				},
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"region": map[string]any{
								"type":   "string",
								"source": directive,
							},
						},
					},
				},
			},
		},
	}
}
