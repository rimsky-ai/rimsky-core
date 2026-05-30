// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dry-run end-to-end scenarios per spec section "Dry-run mode".
// Dry-run is a per-request flag (`?dry_run=true`), not a per-grant
// mode modifier — and it covers EVERY write action with no carve-outs,
// including the auth mutations. Exercises:
//
//   - A write action invoked with `?dry_run=true` returns the synthetic
//     `would_have_*` envelope and does not mutate state; the audit row
//     records `mode:dry_run, executed:false`.
//   - The auth mutations (auth:create / auth:revoke / auth:rotate) are
//     dry-runnable via the flag: they mutate nothing, mint no plaintext,
//     and (for create) return a placeholder id. In anonymous-mode the
//     create dry-run carries the lockdown note.
//   - A read invoked with `?dry_run=true` runs normally (no-op preview)
//     and the audit row records `mode:dry_run, executed:true`.
//
// @concept: dry-run

package auth_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// TestDryRun_AuthCreateMintsNoKey covers the auth:create dry-run branch
// (formerly the K12 "auth mutations are NOT dry-runnable" carve-out,
// now removed): POST /auth/keys?dry_run=true validates the grant,
// returns a `would_have_created_key` envelope with the placeholder id,
// mints NO plaintext, and persists no row.
func TestDryRun_AuthCreateMintsNoKey(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin so we leave anonymous mode and can authoritatively
	// list keys afterward.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Dry-run create: server validates the grant, returns the envelope,
	// and persists nothing.
	code, resp := f.request(t, "POST", "/auth/keys?dry_run=true", adminKey, map[string]any{
		"name":        "previewed-key",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != 200 {
		t.Fatalf("dry-run create: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run create missing dry_run envelope: %+v", resp)
	}
	would, ok := resp["would_have_created_key"].(map[string]any)
	if !ok {
		t.Fatalf("dry-run create missing would_have_created_key: %+v", resp)
	}
	if would["key_id"] != "dry-run-not-persisted" {
		t.Fatalf("dry-run create key_id should be placeholder; got %+v", would)
	}
	if _, leaked := resp["plaintext"]; leaked {
		t.Fatalf("dry-run create must NOT surface a plaintext credential: %+v", resp)
	}
	if _, leaked := would["plaintext"]; leaked {
		t.Fatalf("dry-run create must NOT surface a plaintext credential: %+v", would)
	}

	// The key must not exist — a real GET 404s.
	code, _ = f.request(t, "GET", "/auth/keys/previewed-key", adminKey, nil)
	if code != 404 {
		t.Fatalf("dry-run-previewed key must not be persisted; GET got %d (want 404)", code)
	}
}

// TestDryRun_AuthCreateAnonymousModeNote covers the anonymous-mode
// branch of the auth:create dry-run: when zero active keys exist, the
// envelope carries the lockdown note that committing the first key
// exits anonymous mode.
func TestDryRun_AuthCreateAnonymousModeNote(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Deployment is anonymous (no keys). Dry-run the first key with no
	// Bearer (anonymous-mode probe path).
	code, resp := f.request(t, "POST", "/auth/keys?dry_run=true", "", map[string]any{
		"name":        "first-key",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 200 {
		t.Fatalf("anon dry-run create: %d %+v", code, resp)
	}
	would, ok := resp["would_have_created_key"].(map[string]any)
	if !ok {
		t.Fatalf("anon dry-run create missing would_have_created_key: %+v", resp)
	}
	note, _ := would["note"].(string)
	if note == "" {
		t.Fatalf("anon dry-run create must carry the lockdown note: %+v", would)
	}

	// Still anonymous afterward — nothing was committed.
	code, statusResp := f.request(t, "GET", "/auth/status", "", nil)
	if code != 200 || statusResp["mode"] != "anonymous" {
		t.Fatalf("after anon dry-run create, status should still be anonymous: %d %+v", code, statusResp)
	}
}

// TestDryRun_AuthRevokeMutatesNothing covers the auth:revoke dry-run
// branch: DELETE /auth/keys/{name}?dry_run=true returns the envelope
// and leaves the target key active.
func TestDryRun_AuthRevokeMutatesNothing(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Mint a target key.
	_, tgtBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "victim",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	victimKey := tgtBody["plaintext"].(string)

	// Dry-run revoke: returns the envelope, mutates nothing.
	code, resp := f.request(t, "DELETE", "/auth/keys/victim?dry_run=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("dry-run revoke: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run revoke missing dry_run envelope: %+v", resp)
	}
	if _, ok := resp["would_have_revoked_key"]; !ok {
		t.Fatalf("dry-run revoke missing would_have_revoked_key: %+v", resp)
	}

	// The victim key must still work — the revoke did not execute.
	code, _ = f.request(t, "GET", "/auth/keys", victimKey, nil)
	if code != 200 {
		t.Fatalf("victim key after dry-run revoke: %d (want 200 — revoke must NOT have executed)", code)
	}
}

// TestDryRun_AuthRotateMutatesNothing covers the auth:rotate dry-run
// branch: POST /auth/keys/{name}/rotate?dry_run=true returns the
// envelope, mints no new plaintext, and leaves the old key intact.
func TestDryRun_AuthRotateMutatesNothing(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Mint a rotation target.
	_, tgtBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "rotates-me",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	oldKey := tgtBody["plaintext"].(string)

	// Dry-run rotate.
	code, resp := f.request(t, "POST", "/auth/keys/rotates-me/rotate?dry_run=true", adminKey,
		map[string]any{"grace": "1m"})
	if code != 200 {
		t.Fatalf("dry-run rotate: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run rotate missing dry_run envelope: %+v", resp)
	}
	if _, ok := resp["would_have_rotated_key"]; !ok {
		t.Fatalf("dry-run rotate missing would_have_rotated_key: %+v", resp)
	}
	if _, leaked := resp["plaintext"]; leaked {
		t.Fatalf("dry-run rotate must NOT mint a new plaintext: %+v", resp)
	}

	// The old key still works (it was never revoke_at-stamped).
	code, _ = f.request(t, "GET", "/auth/keys", oldKey, nil)
	if code != 200 {
		t.Fatalf("old key after dry-run rotate: %d (want 200 — rotate must NOT have executed)", code)
	}
}

func TestDryRun_NodeInvalidateReturnsSynthetic(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	// Mint admin.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Seed a real node so the handler reaches the dry-run gate (which
	// fires AFTER validation per spec section "Dry-run mode").
	nodeID := seedDryRunNode(t, f, adminKey)
	// Dry-run is a per-request flag now: pass ?dry_run=true with an
	// ordinary execute-capable key.
	code, resp := f.request(t, "POST", "/nodes/"+nodeID+"/invalidate?dry_run=true", adminKey, map[string]any{
		"reason": "test",
	})
	if code != 200 {
		t.Fatalf("dry-run invalidate: code=%d resp=%+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run envelope missing: %+v", resp)
	}

	// Audit row for the attempt should exist with mode=dry_run +
	// executed=false. List events of kind=auth.access_attempted.
	f.flushAudit()
	ctx := context.Background()
	var foundDryRun int
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if mode, _ := e.Payload["mode"].(string); mode == string(auth.ModeDryRun) {
				foundDryRun++
				if exec, _ := e.Payload["executed"].(bool); exec {
					t.Errorf("dry_run write row has executed=true: %+v", e.Payload)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if foundDryRun == 0 {
		t.Fatalf("expected at least one dry_run audit row")
	}
}

// TestDryRun_ReadIsNoOpExecutedTrue covers the read no-op semantics:
// a `*:read` action invoked with `?dry_run=true` runs normally (returns
// the read body) and the audit row records `mode:dry_run` but
// `executed:true` (the read genuinely ran — there is no mutation to
// skip). Per spec section "Read actions — no-op preview".
func TestDryRun_ReadIsNoOpExecutedTrue(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// A read under ?dry_run=true returns the normal read body (a key
	// listing), NOT a would_have_* envelope.
	code, resp := f.request(t, "GET", "/auth/keys?dry_run=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("dry-run read: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); dr {
		t.Fatalf("read under dry_run must run normally, not return a dry_run envelope: %+v", resp)
	}
	if _, ok := resp["keys"]; !ok {
		t.Fatalf("dry-run read must return the normal read body (keys): %+v", resp)
	}

	// The audit row for this read records mode:dry_run, executed:true.
	f.flushAudit()
	ctx := context.Background()
	var foundReadDryRun bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			action, _ := e.Payload["action"].(string)
			mode, _ := e.Payload["mode"].(string)
			if action != "auth:read" || mode != string(auth.ModeDryRun) {
				continue
			}
			foundReadDryRun = true
			if exec, _ := e.Payload["executed"].(bool); !exec {
				t.Errorf("dry_run READ row should have executed=true (the read ran): %+v", e.Payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !foundReadDryRun {
		t.Fatalf("expected an auth:read audit row with mode=dry_run")
	}
}

// seedDryRunNode registers a single-node template, deploys it, and
// creates an instance — returning a real node id the dry-run test
// can target. The node-invalidate handler only reaches its dry-run
// gate after the node lookup succeeds, so a placeholder UUID would
// short-circuit at the 404 path and never exercise the dry-run code.
//
// Uses the supplied admin key for the (authenticated) seeding
// requests; the caller's test already minted that key.
func seedDryRunNode(t *testing.T, f *authFixture, adminKey string) string {
	t.Helper()
	// Register a minimal template. `name`, `version`, and
	// `frame_resolution_mode` are required by the template
	// validator; the node executor is left empty so the validator's
	// `ExecutorDeclared` hook (nil in this fixture) doesn't reject
	// it.
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":                  "dry-run-seed",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes":                 []map[string]any{{"type": "n1"}},
		},
	}
	code, regResp := f.request(t, "POST", "/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seed template register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seed template register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed template deploy: %d %+v", code, depResp)
	}
	code, instResp := f.request(t, "POST", "/instances", adminKey, map[string]any{"template": hash})
	if code != 201 && code != 200 {
		t.Fatalf("seed instance create: %d %+v", code, instResp)
	}
	instID, _ := instResp["instance_id"].(string)
	if instID == "" {
		t.Fatalf("seed instance missing id: %+v", instResp)
	}
	// List the instance's nodes; the first node is the one we just
	// created.
	code, nodesResp := f.request(t, "GET", "/instances/"+instID+"/nodes", adminKey, nil)
	if code != 200 {
		t.Fatalf("seed list nodes: %d %+v", code, nodesResp)
	}
	nodes, _ := nodesResp["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("seed instance has no nodes: %+v", nodesResp)
	}
	n0, _ := nodes[0].(map[string]any)
	if id, _ := n0["id"].(string); id != "" {
		return id
	}
	t.Fatalf("seed node missing id: %+v", nodes[0])
	return ""
}
