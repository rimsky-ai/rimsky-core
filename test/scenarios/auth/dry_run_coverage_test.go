// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: dry-run

package auth_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type dryRunCase struct {
	method               string
	path                 string
	body                 map[string]any
	headerKey, headerVal string
	wouldHaveKey         string
	verifyNoMutation     func(t *testing.T)
}

func TestDryRunCoverage_AllWriteActions(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", body)
	}

	cases := buildDryRunCases(t, f, adminKey)

	reg := controlapi.BuildV1Registry()
	var writeActions []string
	for _, a := range reg.AllActions() {
		e, _ := reg.Entry(a)
		if e.IsWrite {
			writeActions = append(writeActions, a)
		}
	}
	sort.Strings(writeActions)

	for _, a := range writeActions {
		if _, ok := cases[a]; !ok {
			t.Fatalf("write action %q has no dry-run coverage descriptor; every IsWrite action must be driven with ?dry_run=true and asserted to mutate nothing (the dry-run never-mutates structural guarantee)", a)
		}
	}
	for a := range cases {
		e, ok := reg.Entry(a)
		if !ok {
			t.Fatalf("dry-run coverage descriptor for unknown action %q", a)
		}
		if !e.IsWrite {
			t.Fatalf("dry-run coverage descriptor for non-write action %q", a)
		}
	}
	t.Logf("dry-run coverage: driving %d IsWrite actions with ?dry_run=true", len(writeActions))

	for _, action := range writeActions {
		c := cases[action]
		t.Run(action, func(t *testing.T) {
			code, resp := f.requestWithHeader(t, c.method, c.path, adminKey, c.body, c.headerKey, c.headerVal)
			if code != 200 {
				t.Fatalf("%s: dry-run request returned %d (want 200 dry-run envelope): %+v", action, code, resp)
			}
			if dr, _ := resp["dry_run"].(bool); !dr {
				t.Fatalf("%s: response missing dry_run:true envelope: %+v", action, resp)
			}
			if _, ok := resp[c.wouldHaveKey]; !ok {
				t.Fatalf("%s: response missing %q key: %+v", action, c.wouldHaveKey, resp)
			}
			if c.verifyNoMutation != nil {
				c.verifyNoMutation(t)
			}
		})
	}
}

func buildDryRunCases(t *testing.T, f *authFixture, adminKey string) map[string]dryRunCase {
	t.Helper()
	ctx := context.Background()
	dr := "?dry_run=true"

	tplHash := seedDeployedTemplate(t, f, adminKey, "dryrun-coverage")
	code, instResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{"template": tplHash})
	if code != 201 && code != 200 {
		t.Fatalf("seed instance: %d %+v", code, instResp)
	}
	instanceID := instResp["instance_id"].(string)

	tplHash2 := seedDeployedTemplate(t, f, adminKey, "dryrun-coverage-2")

	code, tagResp := f.request(t, "POST", "/v1/tags", adminKey, map[string]any{
		"tag": "dryrun-tag", "template": tplHash2,
	})
	if code != 201 {
		t.Fatalf("seed tag: %d %+v", code, tagResp)
	}

	bpID := seedBreakpoint(t, f, adminKey, instanceID)

	hitID := seedBreakpointHit(t, f, mustUUID(t, instanceID), mustUUID(t, bpID))

	_, victimBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "dryrun-victim", "permissions": []map[string]any{{"action": "*:read"}},
	})
	victimKey := victimBody["plaintext"].(string)

	registeredOnlyHash := seedRegisteredTemplate(t, f, adminKey, "dryrun-deploy-target")

	resetInstanceID, resetNodeID := seedFailedNodeOnNewInstance(ctx, t, f, adminKey, tplHash)
	_ = resetInstanceID

	assetInstanceID, assetAlias := seedDurableAsset(ctx, t, f)

	debugOverrideInstanceID := seedPausedInstanceForDebugOverride(t, f, tplHash, adminKey)

	keyStillActive := func(name string, key string) func(t *testing.T) {
		return func(t *testing.T) {
			code, _ := f.request(t, "GET", "/v1/auth/keys", key, nil)
			if code != 200 {
				t.Fatalf("key %q must still be usable after dry-run (no mutation); got %d", name, code)
			}
		}
	}
	tagUnchanged := func(tag, wantTemplate string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/tags", adminKey, nil)
			if code != 200 {
				t.Fatalf("tag re-list: %d", code)
			}
			items, _ := resp["tags"].([]any)
			for _, it := range items {
				m := it.(map[string]any)
				if m["tag"] == tag {
					if m["template_id"] != wantTemplate {
						t.Fatalf("tag %q moved under dry-run: %+v (want template %s)", tag, m, wantTemplate)
					}
					return
				}
			}
			t.Fatalf("tag %q vanished under dry-run", tag)
		}
	}
	templateStateIs := func(hash, wantState string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/templates/"+hash, adminKey, nil)
			if code != 200 {
				t.Fatalf("template re-fetch %s: %d %+v", hash, code, resp)
			}
			if st, _ := resp["state"].(string); st != wantState {
				t.Fatalf("template %s state changed under dry-run: got %q want %q", hash, st, wantState)
			}
		}
	}
	instancePausedIs := func(id string, want bool) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/instances/"+id, adminKey, nil)
			if code != 200 {
				t.Fatalf("instance re-fetch: %d %+v", code, resp)
			}
			if p, _ := resp["paused"].(bool); p != want {
				t.Fatalf("instance paused flag changed under dry-run: got %v want %v", p, want)
			}
		}
	}
	instanceNotTerminated := func(id string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/instances/"+id, adminKey, nil)
			if code != 200 {
				t.Fatalf("instance re-fetch: %d %+v", code, resp)
			}
			if _, ok := resp["terminated_at"]; ok {
				t.Fatalf("instance terminated under dry-run: %+v", resp)
			}
		}
	}
	breakpointStillExists := func(id, bp string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/instances/"+id+"/breakpoints", adminKey, nil)
			if code != 200 {
				t.Fatalf("breakpoint re-list: %d", code)
			}
			items, _ := resp["breakpoints"].([]any)
			for _, it := range items {
				if it.(map[string]any)["breakpoint_id"] == bp {
					return
				}
			}
			t.Fatalf("breakpoint %q deleted under dry-run", bp)
		}
	}
	messageCountUnchanged := func(id string) func(t *testing.T) {
		before := listMessageCount(t, f, adminKey, id)
		return func(t *testing.T) {
			after := listMessageCount(t, f, adminKey, id)
			if after != before {
				t.Fatalf("message count changed under dry-run: before=%d after=%d", before, after)
			}
		}
	}
	// @concept: node
	nodeHasFailedRun := func(id string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/nodes/"+id, adminKey, nil)
			if code != 200 {
				t.Fatalf("node re-fetch: %d %+v", code, resp)
			}
			summary, _ := resp["run_summary"].(map[string]any)
			if summary == nil {
				t.Fatalf("node %s missing run_summary on response", id)
			}
			failed, _ := summary["failed_count"].(float64)
			if int(failed) < 1 {
				t.Fatalf("node %s lost its failed terminal run under dry-run: run_summary=%+v", id, summary)
			}
		}
	}
	assetStillExists := func(id, alias string) func(t *testing.T) {
		return func(t *testing.T) {
			code, _ := f.request(t, "GET", "/v1/instances/"+id+"/assets/"+alias, adminKey, nil)
			if code != 200 {
				t.Fatalf("asset %q deleted under dry-run; GET got %d", alias, code)
			}
		}
	}

	return map[string]dryRunCase{
		"instance:create": {
			method: "POST", path: "/v1/instances" + dr,
			body:         map[string]any{"template": tplHash, "instance_key": "dryrun-create-key"},
			wouldHaveKey: "would_have_created",
			verifyNoMutation: func(t *testing.T) {
				code, _ := f.request(t, "GET", "/v1/instances/dryrun-create-key", adminKey, nil)
				if code != 404 {
					t.Fatalf("instance created under dry-run; GET by key got %d (want 404)", code)
				}
			},
		},
		"instance:pause": {
			method: "POST", path: "/v1/instances/" + instanceID + "/pause" + dr,
			wouldHaveKey: "would_have_paused", verifyNoMutation: instancePausedIs(instanceID, false),
		},
		"instance:resume": {
			method: "POST", path: "/v1/instances/" + instanceID + "/resume" + dr,
			wouldHaveKey: "would_have_resumed", verifyNoMutation: instancePausedIs(instanceID, false),
		},
		"instance:kill": {
			method: "POST", path: "/v1/instances/" + instanceID + "/terminate" + dr,
			body:         map[string]any{"reason": "dry-run"},
			wouldHaveKey: "would_have_terminated", verifyNoMutation: instanceNotTerminated(instanceID),
		},
		"instance:terminate": {
			method: "DELETE", path: "/v1/instances/" + seedTerminatedInstanceForDelete(ctx, t, f, tplHash, adminKey) + dr,
			wouldHaveKey: "would_have_terminated",
		},

		"breakpoint:create": {
			method: "POST", path: "/v1/instances/" + instanceID + "/breakpoints" + dr,
			body:         map[string]any{"checkpoint": "before_dispatch"},
			wouldHaveKey: "would_have_created_breakpoint",
			verifyNoMutation: func(t *testing.T) {
				code, resp := f.request(t, "GET", "/v1/instances/"+instanceID+"/breakpoints", adminKey, nil)
				if code != 200 {
					t.Fatalf("breakpoint re-list: %d", code)
				}
				items, _ := resp["breakpoints"].([]any)
				if len(items) != 1 {
					t.Fatalf("breakpoint created under dry-run: list has %d (want 1 seeded)", len(items))
				}
			},
		},
		"breakpoint:delete": {
			method: "DELETE", path: "/v1/instances/" + instanceID + "/breakpoints/" + bpID + dr,
			wouldHaveKey: "would_have_deleted_breakpoint", verifyNoMutation: breakpointStillExists(instanceID, bpID),
		},
		"breakpoint:resume": {
			method: "POST", path: "/v1/instances/" + instanceID + "/breakpoints/" + bpID + "/resume" + dr,
			body:         map[string]any{"hit_id": hitID},
			wouldHaveKey: "would_have_resumed_breakpoint",
		},

		"template:register": {
			method: "POST", path: "/v1/templates" + dr,
			body: map[string]any{"spec": map[string]any{
				"name": "dryrun-register", "version": "1",
				"nodes": []map[string]any{{"type": "n1"}},
			}},
			wouldHaveKey: "would_have_registered",
		},
		"template:deploy": {
			method: "POST", path: "/v1/templates/" + registeredOnlyHash + "/deploy" + dr,
			wouldHaveKey: "would_have_deployed", verifyNoMutation: templateStateIs(registeredOnlyHash, "registered"),
		},
		"template:undeploy": {
			method: "POST", path: "/v1/templates/" + tplHash2 + "/undeploy" + dr,
			wouldHaveKey: "would_have_undeployed", verifyNoMutation: templateStateIs(tplHash2, "deployed"),
		},
		"template:deregister": {
			method: "DELETE", path: "/v1/templates/" + seedRegisteredTemplate(t, f, adminKey, "dryrun-deregister") + dr,
			wouldHaveKey: "would_have_deregistered",
		},

		"tag:create": {
			method: "POST", path: "/v1/tags" + dr,
			body:         map[string]any{"tag": "dryrun-new-tag", "template": tplHash2},
			wouldHaveKey: "would_have_created_tag",
			verifyNoMutation: func(t *testing.T) {
				code, resp := f.request(t, "GET", "/v1/tags", adminKey, nil)
				if code != 200 {
					t.Fatalf("tag re-list: %d", code)
				}
				items, _ := resp["tags"].([]any)
				for _, it := range items {
					if it.(map[string]any)["tag"] == "dryrun-new-tag" {
						t.Fatalf("tag created under dry-run")
					}
				}
			},
		},
		"tag:set": {
			method: "PUT", path: "/v1/tags/dryrun-tag" + dr,
			body:         map[string]any{"template": tplHash},
			wouldHaveKey: "would_have_moved_tag", verifyNoMutation: tagUnchanged("dryrun-tag", tplHash2),
		},
		"tag:delete": {
			method: "DELETE", path: "/v1/tags/dryrun-tag" + dr,
			wouldHaveKey: "would_have_deleted_tag", verifyNoMutation: tagUnchanged("dryrun-tag", tplHash2),
		},

		"node:reset": {
			method: "POST", path: "/v1/nodes/" + resetNodeID + "/reset" + dr,
			wouldHaveKey: "would_have_reset", verifyNoMutation: nodeHasFailedRun(resetNodeID),
		},

		"message:send": {
			method: "POST", path: "/v1/instances/" + instanceID + "/messages" + dr,
			body:      map[string]any{"type": "system/invalidate", "sender": "operator-test"},
			headerKey: "Idempotency-Key", headerVal: "dryrun-msg-key",
			wouldHaveKey: "would_have_sent", verifyNoMutation: messageCountUnchanged(instanceID),
		},

		"lineage:prune": {
			method: "POST", path: "/v1/admin/lineage/prune" + dr,
			body:         map[string]any{"before": time.Now().UTC().Format(time.RFC3339)},
			wouldHaveKey: "would_have_pruned",
		},

		"asset:delete": {
			method: "DELETE", path: "/v1/instances/" + assetInstanceID + "/assets/" + assetAlias + dr,
			wouldHaveKey: "would_have_deleted_asset", verifyNoMutation: assetStillExists(assetInstanceID, assetAlias),
		},

		"instance:debug-override": {
			method: "POST", path: "/v1/instances/" + debugOverrideInstanceID + "/debug/override" + dr,
			body: map[string]any{
				"action":    "invalidate_node",
				"node_type": "n1",
			},
			wouldHaveKey: "would_have_applied_debug_override",
		},

		"auth:create": {
			method: "POST", path: "/v1/auth/keys" + dr,
			body:         map[string]any{"name": "dryrun-created", "permissions": []map[string]any{{"action": "*:read"}}},
			wouldHaveKey: "would_have_created_key",
			verifyNoMutation: func(t *testing.T) {
				code, _ := f.request(t, "GET", "/v1/auth/keys/dryrun-created", adminKey, nil)
				if code != 404 {
					t.Fatalf("auth:create persisted a key under dry-run; GET got %d (want 404)", code)
				}
			},
		},
		"auth:revoke": {
			method: "DELETE", path: "/v1/auth/keys/dryrun-victim" + dr,
			wouldHaveKey: "would_have_revoked_key", verifyNoMutation: keyStillActive("dryrun-victim", victimKey),
		},
		"auth:rotate": {
			method: "POST", path: "/v1/auth/keys/dryrun-victim/rotate" + dr,
			body:         map[string]any{"grace": "1m"},
			wouldHaveKey: "would_have_rotated_key", verifyNoMutation: keyStillActive("dryrun-victim", victimKey),
		},
	}
}

func seedBreakpoint(t *testing.T, f *authFixture, adminKey, instanceID string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/instances/"+instanceID+"/breakpoints", adminKey, map[string]any{
		"checkpoint": "before_dispatch",
	})
	if code != 201 {
		t.Fatalf("seed breakpoint: %d %+v", code, resp)
	}
	return resp["breakpoint_id"].(string)
}

func seedRegisteredTemplate(t *testing.T, f *authFixture, adminKey, name string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/templates", adminKey, map[string]any{
		"spec": map[string]any{
			"name": name, "version": "1",
			"nodes": []map[string]any{{"type": "n1"}},
		},
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed registered template: %d %+v", code, resp)
	}
	return resp["template_id"].(string)
}

func seedTerminatedInstanceForDelete(ctx context.Context, t *testing.T, f *authFixture, tplHash, adminKey string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template": tplHash, "instance_key": "dryrun-delete-target",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed delete-target instance: %d %+v", code, resp)
	}
	id := mustUUID(t, resp["instance_id"].(string))
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return f.db.Tables().Instances().MarkTerminated(ctx, id, tx)
	}); err != nil {
		t.Fatalf("mark delete-target terminated: %v", err)
	}
	return id.String()
}

func seedPausedInstanceForDebugOverride(t *testing.T, f *authFixture, tplHash, adminKey string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template": tplHash, "instance_key": "dryrun-debug-override-target",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed debug-override instance: %d %+v", code, resp)
	}
	id := resp["instance_id"].(string)
	code, pauseResp := f.request(t, "POST", "/v1/instances/"+id+"/pause", adminKey, nil)
	if code != 200 {
		t.Fatalf("pause debug-override instance: %d %+v", code, pauseResp)
	}
	return id
}

func seedBreakpointHit(t *testing.T, f *authFixture, instanceID, bpID foundationshared.UUID) string {
	t.Helper()
	ctx := context.Background()
	var hitID foundationshared.UUID
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := f.db.Tables().BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID,
			InstanceID:   instanceID,
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			HitAt:        time.Now().UTC(),
			Snapshot: map[string]any{
				"checkpoint":       "before_dispatch",
				"dispatch_context": map[string]any{"executor": "worker", "node_type": "n1", "graph": "main"},
				"node_run":         map[string]any{},
				"held_claims":      []any{},
				"open_wait_set":    []any{},
			},
		}, tx)
		if err != nil {
			return err
		}
		hitID = id
		return nil
	}); err != nil {
		t.Fatalf("seed breakpoint hit: %v", err)
	}
	return hitID.String()
}

func seedFailedNodeOnNewInstance(ctx context.Context, t *testing.T, f *authFixture, adminKey, tplHash string) (string, string) {
	t.Helper()
	code, instResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template": tplHash, "instance_key": "dryrun-reset-instance",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed reset instance: %d %+v", code, instResp)
	}
	instanceID := mustUUID(t, instResp["instance_id"].(string))
	code, nodesResp := f.request(t, "GET", "/v1/instances/"+instanceID.String()+"/nodes", adminKey, nil)
	if code != 200 {
		t.Fatalf("seed reset list nodes: %d %+v", code, nodesResp)
	}
	nodes := nodesResp["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("seed reset instance has no nodes")
	}
	nodeID := mustUUID(t, nodes[0].(map[string]any)["id"].(string))

	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := foundationshared.UUID(uuid.New())
		if err := f.db.Tables().Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		_ = nodeID
		rootScope := foundationshared.UUID(uuid.New())
		if err := f.db.Tables().RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         rootScope,
			GraphName:  "main",
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		frameID, err := f.db.Tables().Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, 600000, tx)
		if err != nil {
			return err
		}
		frameRow, err := f.db.Tables().Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		runID := foundationshared.UUID(uuid.New())
		if err := f.db.Tables().RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID:        runID,
			NodeID:       nodeID,
			FrameID:      foundationshared.UUID(frameID),
			RunScopeID:   frameRow.RootRunScopeID,
			ExecutorName: "",
		}); err != nil {
			return err
		}
		return f.db.Tables().RunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFailed, nil)
	}); err != nil {
		t.Fatalf("seed failed node: %v", err)
	}
	return instanceID.String(), nodeID.String()
}

func seedDurableAsset(ctx context.Context, t *testing.T, f *authFixture) (string, string) {
	t.Helper()
	const (
		nodeType     = "asset-node"
		producerName = "asset-store"
		claimAlias   = "primary"
	)
	tplHash := "sha256-" + strings.Repeat("a", 64)
	instanceID := foundationshared.UUID(uuid.New())
	mainScopeID := foundationshared.UUID(uuid.New())
	nodeID := foundationshared.UUID(uuid.New())

	templateSpec := spec.TemplateSpec{
		Name:    "dryrun-asset",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{{
			Type: nodeType,
			ClaimProducers: []spec.NodeClaimProducerRef{{
				Name:     producerName,
				Selector: "static",
				Intent:   "rw",
				Alias:    claimAlias,
				Lifetime: "durable",
			}},
		}},
	}

	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := f.db.Tables().Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     tplHash,
			Spec:   templateSpec,
			State:  persistence.TemplateStateDeployed,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := f.db.Tables().RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		ck := "dryrun-asset-key"
		if _, err := f.db.Tables().Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: tplHash,
			InstanceKey:  &ck,
		}, tx); err != nil {
			return err
		}
		if _, err := f.db.Tables().Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   nodeType,
		}, tx); err != nil {
			return err
		}
		pn := producerName
		intent := "rw"
		claimID := foundationshared.UUID(uuid.New())
		if err := f.db.Tables().ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &pn,
			ClaimScopeData:     []byte(`"asset-scope"`),
			Intent:             &intent,
			HolderSupervisorID: "dryrun-supervisor",
			HolderNodeID:       nodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
			Lifetime:           spec.ClaimLifetimeDurable,
		}, tx); err != nil {
			return err
		}
		return f.db.Tables().ClaimHandles().Promote(ctx, claimID, "dryrun-supervisor",
			spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("seed durable asset: %v", err)
	}
	return instanceID.String(), nodeType + "." + claimAlias
}

func listMessageCount(t *testing.T, f *authFixture, adminKey, instanceID string) int {
	t.Helper()
	code, resp := f.request(t, "GET", "/v1/instances/"+instanceID+"/messages", adminKey, nil)
	if code != 200 {
		t.Fatalf("list messages: %d %+v", code, resp)
	}
	msgs, _ := resp["messages"].([]any)
	return len(msgs)
}

func mustUUID(t *testing.T, s string) foundationshared.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return foundationshared.UUID(id)
}
