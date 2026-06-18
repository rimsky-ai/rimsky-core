// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.


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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestCanary_TemplateRegistrationAndRunAPass(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("root-worker").Success(map[string]any{"phase": "root"}, true, "root-pass")
	h.Stub.WhenType("downstream-worker").Success(map[string]any{"phase": "downstream"}, true, "downstream-pass")

	tmpl := node.TemplateSpec{
		Name:    "canary-template-run-a-pass",
		Version: "v1",
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
					spec.SubscriptionEntry{Node: "root-worker", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				),
			),
		},
	}

	templateHash := h.DeployTemplate(tmpl)
	require.NotEmpty(t, templateHash, "DeployTemplate must return a non-empty template_hash")

	instanceID := createInstanceViaHTTP(t, h, templateHash)
	require.NotEqual(t, uuid.Nil, instanceID, "POST /instances must return a non-nil instance_id")

	rootNode := h.FindNode(instanceID, "root-worker")
	require.NotNil(t, rootNode, "root-worker node must exist on the instance")
	require.True(t, h.WaitForNodeState(rootNode.ID, cascade.NodeStateFresh, 15*time.Second),
		"root-worker did not reach fresh — supervisor/dispatch path broken")

	downstreamNode := h.FindNode(instanceID, "downstream-worker")
	require.NotNil(t, downstreamNode, "downstream-worker node must exist on the instance")
	require.True(t, h.WaitForNodeState(downstreamNode.ID, cascade.NodeStateFresh, 15*time.Second),
		"downstream-worker did not reach fresh — cascade-fire via subscribes: broken")
}

func createInstanceViaHTTP(t *testing.T, h *scenario.Harness, templateHash string) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"template": templateHash,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.ControlBase+"/v1/instances", bytes.NewReader(raw))
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
	// @decision: empty-message-as-root-trigger
	h.PostInstanceMessage(shared.UUID(id), "", nil, "canary-wake-"+id.String())
	return id
}
