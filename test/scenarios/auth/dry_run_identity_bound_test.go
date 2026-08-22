// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: dry-run
// @concept: permission

package auth_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func seedDryRunTemplate(t *testing.T, f *authFixture, adminKey string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "dry-run-identity-bound-seed",
			"version": "1",
			"nodes":   []map[string]any{{"type": "n1"}},
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

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	hash := seedDryRunTemplate(t, f, adminKey)

	if n := countInstances(t, f, adminKey); n != 0 {
		t.Fatalf("expected 0 instances before any create; got %d", n)
	}

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

	if n := countInstances(t, f, adminKey); n != 0 {
		t.Fatalf("mode:dry_run create persisted an instance; GET /instances shows %d (want 0)", n)
	}

	code, resp = f.request(t, "POST", "/v1/instances?dry_run=false", dryKey, map[string]any{"template": hash})
	if code != 200 {
		t.Fatalf("mode:dry_run create (dry_run=false): code=%d resp=%+v (want 200 — an explicit false flag must NOT lift an identity-bound dry-run floor)", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("mode:dry_run create (dry_run=false) missing dry_run envelope: %+v — the identity-bound floor was lifted by an explicit false flag", resp)
	}
	would, ok = resp["would_have_created"].(map[string]any)
	if !ok {
		t.Fatalf("mode:dry_run create (dry_run=false) missing would_have_created: %+v", resp)
	}
	if would["instance_id"] != "dry-run-not-persisted" {
		t.Fatalf("mode:dry_run create (dry_run=false) instance_id should be the placeholder; got %+v", would)
	}
	if n := countInstances(t, f, adminKey); n != 0 {
		t.Fatalf("mode:dry_run create (dry_run=false) persisted an instance; GET /instances shows %d (want 0)", n)
	}

	ctx := context.Background()
	var sawFlooredAttempt bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{KindIn: []string{auth.EventAccessAttempted.String()}}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			action, _ := e.Payload.Map()["action"].(string)
			if action != "instance:create" {
				continue
			}
			mode, _ := e.Payload.Map()["mode"].(string)
			if mode != string(auth.ModeDryRun) {
				continue
			}
			sawFlooredAttempt = true
			if exec, _ := e.Payload.Map()["executed"].(bool); exec {
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

	if n := countInstances(t, f, adminKey); n != 1 {
		t.Fatalf("after execute-mode create, GET /instances shows %d (want 1 — only the execute key committed)", n)
	}
}
