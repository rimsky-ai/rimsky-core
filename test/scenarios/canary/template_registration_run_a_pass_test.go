// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Canary scenario — template-registration + run-a-pass against the
// public control-API surface.
//
// Replaces the drift signal previously carried by `apps/crimefinder/`
// (a TS orchestrator that registered + instantiated a non-trivial
// template against rimsky and asserted control-api stayed up). The
// repo-reorganization spec (`2026-05-24-repo-reorganization-design`
// §P2.5) moves crimefinder to its own sibling repo and adds this
// in-tree canary in its place. Breakage now lands in the PR that
// caused it, instead of being noticed only when crimefinder next
// bumps its rimsky pin.
//
// Scope (audited against `apps/crimefinder/test/integration/full-pass.test.ts`):
//
//   - rimsky's template parser accepts a non-trivial multi-node
//     template via `POST /templates`.
//   - the operator-API deploy verb (`POST /templates/{hash}/deploy`)
//     succeeds.
//   - `POST /instances` returns an `instance_id` for the registered
//     template.
//   - the scheduler + supervisor drive at least one node to its
//     terminal — proving the dispatch and callback wire stayed up
//     across the template-registration → instance-creation → run-a-pass
//     boundary.
//
// What this does NOT cover (left to other scenario tests in `test/scenarios/`):
//   - per-template semantic invariants (cascade timing, fan-out,
//     lifecycle events, etc.).
//   - sensor/publisher message delivery.
//   - error-policy routing.
//
// The canary's job is to surface "rimsky's public surface broke" —
// not to substitute for full e2e coverage of every feature.

package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

// TestCanary_TemplateRegistrationAndRunAPass exercises the full
// public-API path that crimefinder previously canaried: POST template,
// POST deploy, POST instance, observe nodes walk to terminal.
//
// The template carries two nodes (one root + one downstream with a
// node-subscription) so the canary touches at least one cascade-fire
// hop — silent YAML-grammar drift in `subscribes:` parsing would
// surface here as either a control-API 4xx on `/templates` or a
// missing terminal on the downstream node.
func TestCanary_TemplateRegistrationAndRunAPass(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Both nodes use the stub executor and return a Success terminal.
	h.Stub.WhenType("root-worker").Success(map[string]any{"phase": "root"}, true, "root-pass")
	h.Stub.WhenType("downstream-worker").Success(map[string]any{"phase": "downstream"}, true, "downstream-pass")

	// Non-trivial template: two nodes, one node-subscription edge,
	// per-node attributes_schema. This is the smallest shape that
	// exercises the YAML grammar surface a downstream consumer cares
	// about (template_hash content-addressing, deploy verb, instance
	// creation, cascade-fire via subscribes:).
	tmpl := node.TemplateSpec{
		Name:                "canary-template-run-a-pass",
		Version:             "v1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "root-worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"phase": map[string]any{"type": "string"},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream-worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"phase": map[string]any{"type": "string"},
					},
				}),
				scenario.WithSubscribes(
					spec.SubscriptionEntry{Node: "root-worker", Type: "terminal/success"},
				),
			),
		},
	}

	// Register + deploy via the harness shortcut. This mirrors the
	// crimefinder integration test's `registerTemplate` + `deployTemplate`
	// calls, except invoked in-process.
	templateHash := h.DeployTemplate(tmpl)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	// Drive instance creation through the public control-API surface
	// (raw HTTP, same wire shape an external consumer hits). The
	// harness's CreateInstance helper rides the same path; using the
	// raw HTTP form here pins the wire-surface specifically because
	// that's the canary's role.
	instanceID := createInstanceViaHTTP(t, h, templateHash)
	require.NotEqual(t, uuid.Nil, instanceID, "POST /instances must return a non-nil instance_id")

	// Walk: the supervisor should pick up the root-worker node, drive
	// it to terminal, then cascade-fire the downstream-worker via the
	// subscribes: edge.
	rootNode := h.FindNode(instanceID, "root-worker")
	require.NotNil(t, rootNode, "root-worker node must exist on the instance")
	require.True(t, h.WaitForNodeState(rootNode.ID, cascade.NodeStateFresh, 15*time.Second),
		"root-worker did not reach fresh — supervisor/dispatch path broken")

	downstreamNode := h.FindNode(instanceID, "downstream-worker")
	require.NotNil(t, downstreamNode, "downstream-worker node must exist on the instance")
	require.True(t, h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 15*time.Second),
		"downstream-worker did not reach fresh — cascade-fire via subscribes: broken")
}

// createInstanceViaHTTP POSTs to `/instances` against the in-process
// control-API and returns the created instance_id. Asserts 201 +
// non-empty body shape.
func createInstanceViaHTTP(t *testing.T, h *scenario.Harness, templateHash string) uuid.UUID {
	t.Helper()
	// Use the public field name `template` (accepts hash or tag) per
	// `code:control/controlapi/instances.go::createInstanceRequest`.
	body := map[string]any{
		"template": templateHash,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.ControlBase+"/instances", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "POST /instances must reach the control-API")
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"POST /instances expected 201, got %d: %s", resp.StatusCode, string(got))
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.Unmarshal(got, &out), "instance create response must be JSON with instance_id")
	id, err := uuid.Parse(out.InstanceID)
	require.NoErrorf(t, err, "instance_id %q is not a valid UUID", out.InstanceID)
	return id
}
