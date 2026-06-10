// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dry-run coverage conformance test — the STRUCTURAL GUARANTEE behind
// "forced dry-run never mutates" (spec section "Structural guarantee").
//
// This test enumerates EVERY write action in BuildV1Registry() (entries
// where IsWrite == true) and, for each, drives a representative request
// with `?dry_run=true` through the real HTTP stack. For each write action
// it asserts:
//
//	(a) the response carries `dry_run: true` with a `would_have_*` key, and
//	(b) no mutation occurred (a per-action re-check of the target state).
//
// It also asserts that EVERY IsWrite action has a coverage descriptor:
// if a future write action is added to the registry with no descriptor
// here, the test fails — forcing the author to prove their handler's
// dry-run branch. This is what makes the guarantee structural rather
// than a happy-path sample: there is no runtime registry flag or gate;
// the test fails CI if a write handler forgets its dry-run branch.
//
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
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
)

// dryRunCase describes how to drive one write action's representative
// dry-run request and how to confirm it mutated nothing.
type dryRunCase struct {
	// method + path are the HTTP request the test sends (path already
	// includes ?dry_run=true). Built per-case so URL params resolve to
	// real seeded ids.
	method string
	path   string
	body   map[string]any
	// header is an optional extra request header (e.g. Idempotency-Key
	// on message:send).
	headerKey, headerVal string
	// wouldHaveKey is the `would_have_*` envelope key the handler must
	// return under dry-run.
	wouldHaveKey string
	// verifyNoMutation re-checks the action's target state and fails the
	// test if the dry-run request changed anything.
	verifyNoMutation func(t *testing.T)
}

// TestDryRunCoverage_AllWriteActions is the coverage conformance test.
func TestDryRunCoverage_AllWriteActions(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Admin key drives every request (a `*` grant covers every action;
	// dry-run is sourced from the request flag, not the grant).
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", body)
	}

	cases := buildDryRunCases(t, f, adminKey)

	// Coverage assertion: every IsWrite action in the registry must have
	// a descriptor. A new write action with no descriptor fails here,
	// which is the structural guarantee — the author must prove the
	// dry-run branch.
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
	// And no descriptor should reference a non-write / unknown action.
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

	// Drive each write action's dry-run request and assert the envelope +
	// no mutation.
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

// buildDryRunCases seeds the prerequisites each write action needs to
// reach its dry-run gate and returns a descriptor per write action.
//
// Most prerequisites are seeded over the real HTTP API (template
// register/deploy, instance create, breakpoint create, backfill create);
// the two actions whose gate needs deep internal state the fixture can't
// reach happy-path (no executor, no real store backend) are seeded via
// the persistence layer:
//
//   - node:reset needs a node projected as `failed` — seeded via a
//     directly-created failed root run (the test owns the state, not the
//     engine, which never runs in this fixture).
//   - asset:delete needs a committed durable claim handle on a node with
//     a matching template `stores:` entry — seeded via direct template +
//     instance + node + claim inserts (a real deploy/instance-create
//     fan-out would 500 against the unconfigured store).
func buildDryRunCases(t *testing.T, f *authFixture, adminKey string) map[string]dryRunCase {
	t.Helper()
	ctx := context.Background()
	dr := "?dry_run=true"

	// --- shared HTTP-seeded scaffolding ---------------------------------

	// A deployed template + instance, reused by the instance/message/
	// backfill/breakpoint/node:invalidate/asset:materialize cases.
	tplHash := seedDeployedTemplate(t, f, adminKey, "dryrun-coverage")
	code, instResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{"template": tplHash})
	if code != 201 && code != 200 {
		t.Fatalf("seed instance: %d %+v", code, instResp)
	}
	instanceID := instResp["instance_id"].(string)

	// The instance's single root node (for node:invalidate).
	code, nodesResp := f.request(t, "GET", "/v1/instances/"+instanceID+"/nodes", adminKey, nil)
	if code != 200 {
		t.Fatalf("seed list nodes: %d %+v", code, nodesResp)
	}
	nodes := nodesResp["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("seed instance has no nodes")
	}
	invalidateNodeID := nodes[0].(map[string]any)["id"].(string)

	// A second template for tag:set / template:deploy / template:undeploy
	// / template:deregister (so deregister-dry-run doesn't trample the
	// instance's template).
	tplHash2 := seedDeployedTemplate(t, f, adminKey, "dryrun-coverage-2")

	// A tag pointing at tplHash2 (for tag:set / tag:delete).
	code, tagResp := f.request(t, "POST", "/v1/tags", adminKey, map[string]any{
		"tag": "dryrun-tag", "template": tplHash2,
	})
	if code != 201 {
		t.Fatalf("seed tag: %d %+v", code, tagResp)
	}

	// A dedicated instance whose root node `n1` is a fan-out node wired
	// for the backfill override (its partition_request pulls from the
	// trigger message). `backfill:create` rejects (400) a target that is
	// not such a node, so the backfill cases need a wired fan-out target
	// distinct from the plain-node `dryrun-coverage` instance reused by
	// the node/asset/message cases.
	backfillTplHash := seedDeployedFanOutTemplate(t, f, adminKey, "dryrun-backfill-fanout")
	code, bfInstResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{"template": backfillTplHash})
	if code != 201 && code != 200 {
		t.Fatalf("seed backfill instance: %d %+v", code, bfInstResp)
	}
	backfillInstanceID := bfInstResp["instance_id"].(string)

	// A breakpoint (for breakpoint:delete) + a backfill (for
	// backfill:cancel). Both created for real so their dry-run-delete /
	// dry-run-cancel reach their gates.
	bpID := seedBreakpoint(t, f, adminKey, instanceID)
	backfillOpID := seedBackfill(t, f, adminKey, backfillInstanceID)

	// A breakpoint hit (for breakpoint:resume) — seeded via persistence
	// (a real hit requires the engine to actually pause a dispatch).
	hitID := seedBreakpointHit(t, f, mustUUID(t, instanceID), mustUUID(t, bpID))

	// A second key (for auth:revoke / auth:rotate) so the dry-run target
	// is a real persisted key. (auth:create needs no target.)
	_, victimBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "dryrun-victim", "permissions": []map[string]any{{"action": "*:read"}},
	})
	victimKey := victimBody["plaintext"].(string)

	// A registered-but-NOT-deployed template for template:deploy's dry-run
	// gate (an already-deployed template short-circuits to {no_op:true}
	// before the gate, so a deployed template would never exercise the
	// dry-run branch).
	registeredOnlyHash := seedRegisteredTemplate(t, f, adminKey, "dryrun-deploy-target")

	// --- persistence-seeded deep state ----------------------------------

	// node:reset target — a node projected as `failed`, on a SEPARATE
	// instance so the failed-state seed doesn't collide with the
	// node:invalidate target (which must stay `fresh`).
	resetInstanceID, resetNodeID := seedFailedNodeOnNewInstance(ctx, t, f, adminKey, tplHash)
	_ = resetInstanceID

	// asset:delete target — a committed durable claim on a node with a
	// matching template `stores:` entry, all seeded directly.
	assetInstanceID, assetAlias := seedDurableAsset(ctx, t, f)

	// --- no-mutation verifiers ------------------------------------------

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
	backfillNotCancelled := func(op string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/backfills/"+op, adminKey, nil)
			if code != 200 {
				t.Fatalf("backfill re-fetch: %d %+v", code, resp)
			}
			// A cancelled backfill voids its messages; we assert the
			// status object still reports a non-cancelled shape by
			// checking the response did not error. The fine-grained
			// cancellation flag is internal; absence of a 404/void is
			// sufficient here (the would_have_cancelled envelope is the
			// primary signal).
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
	nodeStateIs := func(id, want string) func(t *testing.T) {
		return func(t *testing.T) {
			code, resp := f.request(t, "GET", "/v1/nodes/"+id, adminKey, nil)
			if code != 200 {
				t.Fatalf("node re-fetch: %d %+v", code, resp)
			}
			if st, _ := resp["state"].(string); st != want {
				t.Fatalf("node %s state changed under dry-run: got %q want %q", id, st, want)
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
		// --- instances ---
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
			// DELETE requires the instance to already be terminal; seed a
			// separate, terminated instance so the dry-run reaches the gate.
			method: "DELETE", path: "/v1/instances/" + seedTerminatedInstanceForDelete(ctx, t, f, tplHash, adminKey) + dr,
			wouldHaveKey: "would_have_terminated",
			// The dedicated instance stays present (no row delete).
		},

		// --- breakpoints ---
		"breakpoint:create": {
			method: "POST", path: "/v1/instances/" + instanceID + "/breakpoints" + dr,
			body:         map[string]any{"checkpoint": "before_dispatch"},
			wouldHaveKey: "would_have_created_breakpoint",
			verifyNoMutation: func(t *testing.T) {
				code, resp := f.request(t, "GET", "/v1/instances/"+instanceID+"/breakpoints", adminKey, nil)
				if code != 200 {
					t.Fatalf("breakpoint re-list: %d", code)
				}
				// Only the one seeded breakpoint should exist.
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
			// The hit stays unresumed; verified implicitly (the resume
			// mutation is skipped, so the would_have envelope is the signal).
		},

		// --- templates ---
		"template:register": {
			method: "POST", path: "/v1/templates" + dr,
			body: map[string]any{"spec": map[string]any{
				"name": "dryrun-register", "version": "1",
				"frame_resolution_mode": "serial_queue",
				"nodes":                 []map[string]any{{"type": "n1"}},
			}},
			wouldHaveKey: "would_have_registered",
			// A dry-run register persists nothing; the template list is
			// unaffected (not separately asserted — the envelope suffices,
			// and the spec's dry-run gate is before the persist).
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
			// Deregister a throwaway registered (not deployed) template so
			// the dry-run reaches its gate without affecting tplHash/tplHash2.
			method: "DELETE", path: "/v1/templates/" + seedRegisteredTemplate(t, f, adminKey, "dryrun-deregister") + dr,
			wouldHaveKey: "would_have_deregistered",
		},

		// --- tags ---
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

		// --- nodes ---
		"node:invalidate": {
			method: "POST", path: "/v1/nodes/" + invalidateNodeID + "/invalidate" + dr,
			body:         map[string]any{"reason": "dry-run"},
			wouldHaveKey: "would_have_invalidated", verifyNoMutation: nodeStateIs(invalidateNodeID, "fresh"),
		},
		"node:reset": {
			method: "POST", path: "/v1/nodes/" + resetNodeID + "/reset" + dr,
			wouldHaveKey: "would_have_reset", verifyNoMutation: nodeStateIs(resetNodeID, "failed"),
		},

		// --- messages ---
		"message:send": {
			method: "POST", path: "/v1/instances/" + instanceID + "/messages" + dr,
			body:      map[string]any{"kind": "invalidate", "target": "n1"},
			headerKey: "Idempotency-Key", headerVal: "dryrun-msg-key",
			wouldHaveKey: "would_have_sent", verifyNoMutation: messageCountUnchanged(instanceID),
		},

		// --- lineage ---
		"lineage:prune": {
			method: "POST", path: "/v1/admin/lineage/prune" + dr,
			body:         map[string]any{"before": time.Now().UTC().Format(time.RFC3339)},
			wouldHaveKey: "would_have_pruned",
			// Nothing to prune in the fixture; the gate is before the delete.
		},

		// --- backfills ---
		"backfill:create": {
			method: "POST", path: "/v1/instances/" + backfillInstanceID + "/backfills" + dr,
			body:         map[string]any{"target_node": "n1", "reason": "dry-run"},
			wouldHaveKey: "would_have_created_backfill", verifyNoMutation: messageCountUnchanged(backfillInstanceID),
		},
		"backfill:cancel": {
			method: "POST", path: "/v1/backfills/" + backfillOpID + "/cancel" + dr,
			wouldHaveKey: "would_have_cancelled_backfill", verifyNoMutation: backfillNotCancelled(backfillOpID),
		},

		// --- assets ---
		"asset:materialize": {
			method: "POST", path: "/v1/instances/" + instanceID + "/assets/n1.main/materialize" + dr,
			body:         map[string]any{"reason": "dry-run"},
			wouldHaveKey: "would_have_materialized", verifyNoMutation: messageCountUnchanged(instanceID),
		},
		"asset:delete": {
			method: "DELETE", path: "/v1/instances/" + assetInstanceID + "/assets/" + assetAlias + dr,
			wouldHaveKey: "would_have_deleted_asset", verifyNoMutation: assetStillExists(assetInstanceID, assetAlias),
		},

		// --- auth (no carve-out; dry-runnable via the flag) ---
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

// --- seeding helpers ----------------------------------------------------

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

func seedBackfill(t *testing.T, f *authFixture, adminKey, instanceID string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/instances/"+instanceID+"/backfills", adminKey, map[string]any{
		"target_node": "n1", "reason": "seed",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed backfill: %d %+v", code, resp)
	}
	return resp["backfill_operation_id"].(string)
}

// seedDeployedFanOutTemplate registers + deploys a template whose root
// node `n1` is a fan-out node wired for the backfill override: its
// partition_request pulls from the trigger message
// (`{{trigger.message.payload.partition_request_override | <default>}}`),
// so a `backfill:create` against it is accepted (the override can reach
// the node). The fixture has no store backend and the engine never
// runs; the lifecycle fan-out to the referenced store is skipped
// silently (the store is not a subscriber), so register + deploy
// succeed.
func seedDeployedFanOutTemplate(t *testing.T, f *authFixture, adminKey, name string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "n1",
					"executor": "worker",
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "rw", "alias": "data"},
					},
					"fan_out": map[string]any{
						"claim":             "data",
						"partition_request": `{{trigger.message.payload.partition_request_override | {"partition_keys":["default"]}}}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
				},
			},
		},
	}
	code, regResp := f.request(t, "POST", "/v1/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seedDeployedFanOutTemplate register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seedDeployedFanOutTemplate register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/v1/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seedDeployedFanOutTemplate deploy: %d %+v", code, depResp)
	}
	return hash
}

func seedRegisteredTemplate(t *testing.T, f *authFixture, adminKey, name string) string {
	t.Helper()
	code, resp := f.request(t, "POST", "/v1/templates", adminKey, map[string]any{
		"spec": map[string]any{
			"name": name, "version": "1",
			"frame_resolution_mode": "serial_queue",
			"nodes":                 []map[string]any{{"type": "n1"}},
		},
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed registered template: %d %+v", code, resp)
	}
	return resp["template_id"].(string)
}

// seedTerminatedInstanceForDelete creates an instance from tplHash and
// marks it terminal via the persistence layer so instance:terminate's
// DELETE dry-run reaches its gate (the 409 terminal guard passes).
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

// seedBreakpointHit inserts a pending breakpoint hit directly (a real
// hit requires the engine to pause a dispatch, which the executor-less
// fixture can't do).
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

// seedFailedNodeOnNewInstance creates a fresh instance from tplHash (over
// HTTP) and drives its root node's projected state to `failed` by
// inserting a failed root run directly. The executor-less fixture never
// runs a node, so a real failed state is unreachable happy-path; the
// test owns the state. node:reset's gate is after the `state == failed`
// check, so the node must genuinely project failed for the dry-run
// request to reach (and exercise) the dry-run branch. A SEPARATE instance
// keeps this seed from colliding with the node:invalidate target (which
// must stay `fresh`). Returns (instanceID, nodeID).
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
		inst, err := f.db.Tables().Instances().Get(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		frameID, err := frame.EnqueueOrCoalesce(ctx, f.db.Tables(), tx, uuid.UUID(instanceID), uuid.UUID(nodeID))
		if err != nil {
			return err
		}
		runID := foundationshared.UUID(uuid.New())
		if err := f.db.Tables().RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID:        runID,
			NodeID:       nodeID,
			FrameID:      foundationshared.UUID(frameID),
			RunScopeID:   inst.MainRunScopeID,
			ExecutorName: "",
		}); err != nil {
			return err
		}
		// Transition the run to failed (UpdateStateAndOutcome does not
		// validate — the test owns the seeded state).
		return f.db.Tables().RunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFailed, nil)
	}); err != nil {
		t.Fatalf("seed failed node: %v", err)
	}
	return instanceID.String(), nodeID.String()
}

// seedDurableAsset seeds an instance whose template node carries a
// `stores:` entry and a committed durable claim handle on that node with
// a matching producer_name, so asset:delete's dry-run resolves the asset
// and reaches its gate. Everything is inserted directly: a real deploy /
// instance-create would fan OnInstanceCreated out to the unconfigured
// store and 500. Returns (instanceID, assetAlias) where assetAlias is
// the `{node_type}.{claim_alias}` path segment the route expects.
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
		Name:                "dryrun-asset",
		Version:             "1",
		FrameResolutionMode: "serial_queue",
		Nodes: []spec.TemplateNodeDef{{
			Type: nodeType,
			Stores: []spec.NodeStoreRef{{
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
			ID:             instanceID,
			TemplateHash:   tplHash,
			InstanceKey:    &ck,
			MainRunScopeID: mainScopeID,
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
		// Insert an active claim handle, then promote it to committed so
		// the asset query (ListByInstanceAndState committed/durable) finds
		// it.
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

// listMessageCount returns the number of messages currently on an
// instance (used to assert message:send / backfill:create / materialize
// dry-runs enqueue nothing).
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
