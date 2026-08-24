// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestHandler_GetInstance_CascadeGraphIncludesEdges(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()

	tmplSpec := spec.TemplateSpec{
		Name:    "cascade-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "upstream", Executor: "test-executor"},
			{Type: "downstream", Executor: "test-executor", Subscribes: []spec.SubscriptionEntry{
				{Node: "upstream", Type: "attribute"},
			}},
		},
	}
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	upstreamID := shared.UUID(uuid.New())
	downstreamID := shared.UUID(uuid.New())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplSpec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         upstreamID,
			InstanceID: instanceID,
			NodeType:   "upstream",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         downstreamID,
			InstanceID: instanceID,
			NodeType:   "downstream",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/instances/%s", instanceID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		CascadeGraph []struct {
			NodeType string   `json:"node_type"`
			EdgesIn  []string `json:"edges_in"`
			EdgesOut []string `json:"edges_out"`
		} `json:"cascade_graph"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.CascadeGraph) != 2 {
		t.Fatalf("cascade_graph nodes = %d, want 2: %+v", len(body.CascadeGraph), body.CascadeGraph)
	}
	byType := map[string]struct {
		EdgesIn  []string
		EdgesOut []string
	}{}
	for _, n := range body.CascadeGraph {
		byType[n.NodeType] = struct {
			EdgesIn  []string
			EdgesOut []string
		}{n.EdgesIn, n.EdgesOut}
	}
	downstream, ok := byType["downstream"]
	if !ok {
		t.Fatalf("cascade_graph missing downstream node: %+v", body.CascadeGraph)
	}
	if len(downstream.EdgesIn) != 1 || downstream.EdgesIn[0] != "upstream" {
		t.Fatalf("downstream.edges_in = %v, want [upstream]", downstream.EdgesIn)
	}
	upstream, ok := byType["upstream"]
	if !ok {
		t.Fatalf("cascade_graph missing upstream node: %+v", body.CascadeGraph)
	}
	if len(upstream.EdgesOut) != 1 || upstream.EdgesOut[0] != "downstream" {
		t.Fatalf("upstream.edges_out = %v, want [downstream]", upstream.EdgesOut)
	}
}

func TestHandler_GetInstance_NotFound(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/instances/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}
