// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Identity-bound dry-run end-to-end scenario per spec section
// "S-auth-identity-bound-dryrun". Dry-run is NOT only a per-request flag:
// a grant entry may pin `mode: dry_run` on a write action, making the
// key preview-but-never-commit that action regardless of the
// `?dry_run=true` flag. The grant mode is a FLOOR — the caller cannot
// escalate past it by omitting the flag.
//
// This proves the attempt-only behavior is carried by the key's
// identity, not the request flag:
//
//   - A key whose grant carries `{action:"instance:create", mode:"dry_run"}`
//     POSTing /instances WITHOUT ?dry_run=true returns the synthetic
//     `would_have_created` envelope (instance_id="dry-run-not-persisted"),
//     persists no instance row, and lands an `auth.access_attempted`
//     audit row recording mode=dry_run, executed=false.
//   - A SECOND ordinary execute-capable key (grant carries no mode)
//     POSTing /instances WITHOUT the flag really creates the instance —
//     so the preview-only behavior is identity-bound, not flag-bound.
//
// @concept: dry-run
// @concept: permission

package auth_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// seedDryRunTemplate registers and deploys the single-node template the
// identity-bound dry-run scenario instantiates against, returning its
// template hash. It reuses the seedDryRunNode template shape
// (name/version/frame_resolution_mode + one {type:"n1"} node) but stops
// before instance creation — the test drives /instances itself with the
// mode-bound key so the dry-run gate is exercised on the real path.
func seedDryRunTemplate(t *testing.T, f *authFixture, adminKey string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":                  "dry-run-identity-bound-seed",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes":                 []map[string]any{{"type": "n1"}},
		},
	}
	code, regResp := f.request(t, "POST", "/v1/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seed template register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seed template register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/v1/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed template deploy: %d %+v", code, depResp)
	}
	return hash
}

// countInstances returns the number of instance rows the admin key can
// see via GET /instances. Used to prove the dry-run create persisted
// nothing and the subsequent execute-mode create persisted exactly one.
func countInstances(t *testing.T, f *authFixture, adminKey string) int {
	t.Helper()
	code, resp := f.request(t, "GET", "/v1/instances", adminKey, nil)
	if code != 200 {
		t.Fatalf("GET /instances: %d %+v", code, resp)
	}
	list, _ := resp["instances"].([]any)
	return len(list)
}

func TestDryRun_IdentityBoundFloor(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin via the anonymous path so we can mint scoped keys and
	// authoritatively list instances afterward.
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	// Register + deploy the template the scenario instantiates against.
	hash := seedDryRunTemplate(t, f, adminKey)

	// Baseline: no instances yet.
	if n := countInstances(t, f, adminKey); n != 0 {
		t.Fatalf("expected 0 instances before any create; got %d", n)
	}

	// Mint a key whose grant pins instance:create to dry_run (attempt-only
	// floor) and otherwise grants reads. This key can preview a create but
	// can never commit one — the floor is on the identity, not the flag.
	_, dryBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "attempt-only",
		"permissions": []map[string]any{
			{"action": "instance:create", "mode": "dry_run"},
			{"action": "*:read"},
		},
	})
	dryKey, _ := dryBody["plaintext"].(string)
	if dryKey == "" {
		t.Fatalf("attempt-only key plaintext missing: %+v", dryBody)
	}

	// POST /instances with the mode:dry_run key and NO ?dry_run flag. The
	// identity-bound floor must force preview: 200 + synthetic envelope.
	code, resp := f.request(t, "POST", "/v1/instances", dryKey, map[string]any{"template": hash})
	if code != 200 {
		t.Fatalf("mode:dry_run create (no flag): code=%d resp=%+v (want 200 — identity-bound dry-run floor)", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("mode:dry_run create missing dry_run envelope: %+v", resp)
	}
	would, ok := resp["would_have_created"].(map[string]any)
	if !ok {
		t.Fatalf("mode:dry_run create missing would_have_created: %+v", resp)
	}
	if would["instance_id"] != "dry-run-not-persisted" {
		t.Fatalf("mode:dry_run create instance_id should be the placeholder; got %+v", would)
	}

	// No instance row was persisted — the floor held even without the flag.
	if n := countInstances(t, f, adminKey); n != 0 {
		t.Fatalf("mode:dry_run create persisted an instance; GET /instances shows %d (want 0)", n)
	}

	// The auth.access_attempted audit row for instance:create records the
	// floored write: mode=dry_run, executed=false.
	f.flushAudit()
	ctx := context.Background()
	var sawFlooredAttempt bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			action, _ := e.Payload["action"].(string)
			if action != "instance:create" {
				continue
			}
			mode, _ := e.Payload["mode"].(string)
			if mode != string(auth.ModeDryRun) {
				continue
			}
			sawFlooredAttempt = true
			if exec, _ := e.Payload["executed"].(bool); exec {
				t.Errorf("floored instance:create audit row has executed=true: %+v", e.Payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !sawFlooredAttempt {
		t.Fatalf("expected an instance:create audit row with mode=dry_run, executed=false")
	}

	// Mint a SECOND ordinary key with an execute-capable instance:create
	// grant (no mode). With NO flag it must really create the instance —
	// proving attempt-only is carried by the first key's identity, not by
	// the request flag.
	_, execBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "execute-capable",
		"permissions": []map[string]any{
			{"action": "instance:create"},
			{"action": "*:read"},
		},
	})
	execKey, _ := execBody["plaintext"].(string)
	if execKey == "" {
		t.Fatalf("execute-capable key plaintext missing: %+v", execBody)
	}

	code, execResp := f.request(t, "POST", "/v1/instances", execKey, map[string]any{"template": hash})
	if code != 201 && code != 200 {
		t.Fatalf("execute-mode create (no flag): code=%d resp=%+v (want 201 — ordinary key commits)", code, execResp)
	}
	if instID, _ := execResp["instance_id"].(string); instID == "" || instID == "dry-run-not-persisted" {
		t.Fatalf("execute-mode create must return a real persisted instance_id; got %+v", execResp)
	}

	// GET /instances now shows exactly the one committed instance — proving
	// the dry-run floor was identity-bound (the first key committed
	// nothing; the second key, same flag-absent request, committed).
	if n := countInstances(t, f, adminKey); n != 1 {
		t.Fatalf("after execute-mode create, GET /instances shows %d (want 1 — only the execute key committed)", n)
	}
}
