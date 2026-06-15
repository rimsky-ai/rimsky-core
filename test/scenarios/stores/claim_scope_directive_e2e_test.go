// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Acceptance gate AG-TEMPLCASCADE-1 — story
// S-template-validation-claim-scope-end-to-end.
//
// Proves the scope→claim_scope rename is real end to end against the
// REAL assembled product (control-api + scheduler + supervisor on
// testcontainers Postgres via scenario.Start, a real claim-producer
// over loopback gRPC, and a real stub executor):
//
//	(1) registration: a template whose node acquires a claim under alias
//	    `a` and sets `region: "{{claim.a.claim_scope}}"` REGISTERS
//	    successfully (HTTP 2xx, not 400) — the validator admits the
//	    canonical spelling.
//	(2) runtime: an instance of that template dispatches and the executor
//	    receives `Attributes["region"]` equal to the stringified bytes of
//	    the live claim's ClaimScope returned by the producer — the
//	    resolver threads the live claim-scope through the full value path.
//	(3) registration rejection: a sibling deploy of the same template
//	    using the legacy spelling `{{claim.a.scope}}` is REJECTED at
//	    registration with HTTP 400 and a validation error naming the
//	    canonical `claim_scope` segment.
//
// The value-delivering components — validator (registration), resolver
// (dispatch), the producer returning a known ClaimScope, and the
// executor receiving the resolved attribute on a real dispatch — are all
// real; nothing is stubbed in place of the thing under test.
//
// The stub producer (scoped-direct mode, no pick policy) echoes the
// selector verbatim as the ClaimScope: for selector "/scope-A" it
// returns ClaimScope = json.Marshal("/scope-A") = "\"/scope-A\"", which
// the resolver's stringifyRaw collapses to the JSON-string value
// "/scope-A". That is the live value asserted on the wire below — read
// from the producer's actual response, not hard-coded into rimsky.
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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// selectorScopeA is the selector the worker acquires its claim against.
// In the stub producer's scoped-direct mode the selector is echoed
// verbatim as the ClaimScope, so the live claim-scope the resolver
// surfaces is exactly this string.
const selectorScopeA = "/scope-A"

func TestAcceptance_ClaimScopeEndToEnd(t *testing.T) {
	t.Parallel()

	// @deliberate: A real claim-producer over loopback gRPC advertising sync write-
	// semantics. In scoped-direct mode (no pick policy) Open returns
	// ClaimScope = json.Marshal(selector) for selector "/scope-A".
	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"content": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: syncCaps,
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "scenario")

	// @deliberate: (1) registration of the canonical {{claim.a.claim_scope}}
	// spelling succeeds (DeployTemplate fatals on any non-2xx, so a
	// successful return IS the not-400 assertion).
	tid := h.DeployTemplate(claimScopeTemplate("claim-scope-e2e", "{{claim.a.claim_scope}}"))

	iid := h.CreateInstance(tid, "ck-claim-scope-e2e", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker did not reach fresh (acquisition + dispatch + terminal must succeed end to end)")

	// @deliberate: (2) the executor received the directive resolved to the live
	// claim's claim-scope bytes (stringified). The stub producer echoes
	// the selector as the ClaimScope, so the live value is the selector
	// string itself — read off the real dispatch, not hard-coded into
	// rimsky's resolution path.
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

	// @deliberate: (3) a sibling deploy of the SAME template using the legacy
	// spelling {{claim.a.scope}} is rejected at registration with HTTP
	// 400 and a validation error naming the canonical claim_scope
	// segment. Raw POST so the rejection status is observable (the
	// DeployTemplate helper would fatal the test on a non-2xx).
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

// claimScopeTemplate builds a single-node template whose `worker` node
// acquires a claim from the "content" store under alias `a` against
// selector "/scope-A" and binds its `region` attribute to the supplied
// claim directive. Reused across both spellings so the only difference
// under test is the directive's second segment.
func claimScopeTemplate(name, directive string) node.TemplateSpec {
	return node.TemplateSpec{
		Name: name, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("content", selectorScopeA, "rw", "a")),
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

// claimScopeTemplateJSON renders the same template as the snake_case
// wire body POST /templates accepts, so the legacy-spelling rejection
// case can be issued as a raw HTTP POST (the typed DeployTemplate helper
// fatals on a non-2xx and so cannot observe the 400). Mirrors the
// serializer the harness uses for the canonical-spelling path.
func claimScopeTemplateJSON(name, directive string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "1",
		"nodes": []any{
			map[string]any{
				"type":     "worker",
				"executor": "stub",
				"stores": []any{
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
