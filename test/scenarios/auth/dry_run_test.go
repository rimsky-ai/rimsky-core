// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: dry-run

package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestDryRun_AuthCreateMintsNoKey(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	code, resp := f.request(t, "POST", "/v1/auth/keys?dry_run=true", adminKey, map[string]any{
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

	code, _ = f.request(t, "GET", "/v1/auth/keys/previewed-key", adminKey, nil)
	if code != 404 {
		t.Fatalf("dry-run-previewed key must not be persisted; GET got %d (want 404)", code)
	}
}

func TestDryRun_AuthCreateAnonymousModeNote(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	code, resp := f.request(t, "POST", "/v1/auth/keys?dry_run=true", "", map[string]any{
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

	code, statusResp := f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != 200 || statusResp["mode"] != "anonymous" {
		t.Fatalf("after anon dry-run create, status should still be anonymous: %d %+v", code, statusResp)
	}
}

func TestDryRun_AuthRevokeMutatesNothing(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	_, tgtBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "victim",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	victimKey := tgtBody["plaintext"].(string)

	code, resp := f.request(t, "DELETE", "/v1/auth/keys/victim?dry_run=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("dry-run revoke: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run revoke missing dry_run envelope: %+v", resp)
	}
	if _, ok := resp["would_have_revoked_key"]; !ok {
		t.Fatalf("dry-run revoke missing would_have_revoked_key: %+v", resp)
	}

	code, _ = f.request(t, "GET", "/v1/auth/keys", victimKey, nil)
	if code != 200 {
		t.Fatalf("victim key after dry-run revoke: %d (want 200 — revoke must NOT have executed)", code)
	}
}

func TestDryRun_AuthRotateMutatesNothing(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	_, tgtBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "rotates-me",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	oldKey := tgtBody["plaintext"].(string)

	code, resp := f.request(t, "POST", "/v1/auth/keys/rotates-me/rotate?dry_run=true", adminKey,
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

	code, _ = f.request(t, "GET", "/v1/auth/keys", oldKey, nil)
	if code != 200 {
		t.Fatalf("old key after dry-run rotate: %d (want 200 — rotate must NOT have executed)", code)
	}
}

func TestDryRun_ValidationActuallyRuns(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	code, resp := f.request(t, "POST", "/v1/auth/keys?dry_run=true", adminKey, map[string]any{
		"name":        "",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code < 400 || code >= 500 {
		t.Fatalf("dry-run with empty name must surface a 4xx validation error (got %d %+v) — a canned envelope here means the handler bypassed validation, which is the spec's falsifier arm", code, resp)
	}
	if _, ok := resp["would_have_created_key"]; ok {
		t.Fatalf("dry-run with empty name must NOT return a would_have_* envelope (got %+v) — validation MUST run before the dry-run gate", resp)
	}
	if errMsg, _ := resp["error"].(string); errMsg == "" {
		t.Fatalf("dry-run validation failure must surface an error body (got %+v)", resp)
	} else if !strings.Contains(errMsg, "name") {
		t.Fatalf("dry-run error must reflect the empty-name input (got %q) — generic / canned errors fail the falsifier", errMsg)
	}

	code, resp = f.request(t, "POST", "/v1/auth/keys?dry_run=true", adminKey, map[string]any{
		"name":        "would-have-failed-validation",
		"permissions": []map[string]any{{"action": "node:bogus-verb"}},
	})
	if code < 400 || code >= 500 {
		t.Fatalf("dry-run with unknown action must surface a 4xx (got %d %+v) — a canned envelope here means validation didn't run", code, resp)
	}
	if _, ok := resp["would_have_created_key"]; ok {
		t.Fatalf("dry-run with unknown action must NOT return a would_have_* envelope: %+v", resp)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "node:bogus-verb") {
		t.Fatalf("dry-run validation error must name the offending action (got %q) — the handler must have inspected our inputs, not returned a canned response", errMsg)
	}
	code, _ = f.request(t, "GET", "/v1/auth/keys/would-have-failed-validation", adminKey, nil)
	if code != 404 {
		t.Fatalf("dry-run validation failure must not persist a row; GET got %d (want 404)", code)
	}

	_, tgtBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "rotation-target",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if tgtBody["plaintext"].(string) == "" {
		t.Fatalf("seed rotation target: %+v", tgtBody)
	}
	code, resp = f.request(t, "POST", "/v1/auth/keys/rotation-target/rotate?dry_run=true", adminKey, map[string]any{
		"grace": "not-a-duration",
	})
	if code < 400 || code >= 500 {
		t.Fatalf("dry-run with unparseable grace must surface a 4xx (got %d %+v) — a canned envelope here means validation didn't run", code, resp)
	}
	if _, ok := resp["would_have_rotated_key"]; ok {
		t.Fatalf("dry-run with unparseable grace must NOT return a would_have_* envelope: %+v", resp)
	}
	errMsg, _ = resp["error"].(string)
	if !strings.Contains(errMsg, "grace") {
		t.Fatalf("dry-run validation error must mention the grace field (got %q) — generic errors don't prove validation inspected the inputs", errMsg)
	}
}

func TestDryRun_ReadIsNoOpExecutedTrue(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	code, resp := f.request(t, "GET", "/v1/auth/keys?dry_run=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("dry-run read: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); dr {
		t.Fatalf("read under dry_run must run normally, not return a dry_run envelope: %+v", resp)
	}
	if _, ok := resp["keys"]; !ok {
		t.Fatalf("dry-run read must return the normal read body (keys): %+v", resp)
	}

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
