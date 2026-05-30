// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// GET /audit read-surface scenarios per spec section "Audit read
// surface" (spec 2026-05-29-console-upstream-auth-audit-and-fixes).
// /audit reads the auth.* slice of the event log, gated by the
// audit:read action (distinct from event:read). It is filterable by
// actor (key_id / key_name), action (exact / prefix), target (the
// audited request path), result (status), and mode. Exercises:
//
//   - An audit:read key can list and filter /audit; each filter narrows.
//   - A key WITHOUT audit:read gets 403 (standard auth gate).
//
// @concept: event-log
// @concept: permission

package auth_test

import (
	"testing"
)

// auditRows runs GET /audit with the given query string under key and
// returns the decoded `audit` array (one map per row).
func auditRows(t *testing.T, f *authFixture, key, query string) []map[string]any {
	t.Helper()
	code, body := f.request(t, "GET", "/audit"+query, key, nil)
	if code != 200 {
		t.Fatalf("GET /audit%s: %d %+v", query, code, body)
	}
	raw, _ := body["audit"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			if p, ok := m["payload"].(map[string]any); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func TestAuditRead_FilterAndGate(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Bootstrap an admin via anonymous mode. This emits one
	// auth.access_attempted row (POST /auth/keys, mode:execute).
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// Mint a key that has audit:read (the reader under test) and an
	// actor key that has only auth:read (it will generate filterable
	// rows but cannot read /audit).
	const readerName = "audit-reader"
	code, readerBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        readerName,
		"permissions": []map[string]any{{"action": "audit:read"}, {"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	const actorName = "audit-actor"
	code, actorBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        actorName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint actor: %d %+v", code, actorBody)
	}
	actorKey, _ := actorBody["plaintext"].(string)

	// Generate audit rows under the actor key:
	//   - one execute-mode GET /auth/keys (status 200)
	//   - one dry_run-mode GET /auth/keys (status 200, read no-op)
	if st, _ := f.request(t, "GET", "/auth/keys", actorKey, nil); st != 200 {
		t.Fatalf("actor execute read: %d", st)
	}
	if st, _ := f.request(t, "GET", "/auth/keys?dry_run=true", actorKey, nil); st != 200 {
		t.Fatalf("actor dry-run read: %d", st)
	}

	// A key WITHOUT audit:read is denied (403). The actor key has only
	// auth:read.
	if st, body := f.request(t, "GET", "/audit", actorKey, nil); st != 403 {
		t.Fatalf("actor without audit:read should be 403, got %d %+v", st, body)
	}

	// Filter by actor key_name: only the actor's rows.
	got := auditRows(t, f, readerKey, "?key_name="+actorName)
	if len(got) == 0 {
		t.Fatalf("key_name filter returned no rows for actor %q", actorName)
	}
	for _, p := range got {
		if kn, _ := p["key_name"].(string); kn != actorName {
			t.Fatalf("key_name filter leaked a row for %q", kn)
		}
	}

	// Filter by action exact: every returned row is auth:read.
	got = auditRows(t, f, readerKey, "?action=auth:read")
	if len(got) == 0 {
		t.Fatalf("action=auth:read returned no rows")
	}
	for _, p := range got {
		if a, _ := p["action"].(string); a != "auth:read" {
			t.Fatalf("action filter leaked a row for action %q", a)
		}
	}

	// Filter by action prefix: auth:* covers auth:read + auth:create rows.
	prefixRows := auditRows(t, f, readerKey, "?action_prefix=auth:")
	if len(prefixRows) == 0 {
		t.Fatalf("action_prefix=auth: returned no rows")
	}
	for _, p := range prefixRows {
		a, _ := p["action"].(string)
		if len(a) < 5 || a[:5] != "auth:" {
			t.Fatalf("action_prefix leaked a row for action %q", a)
		}
	}

	// Filter by mode=dry_run: only the actor's dry-run read (mode:dry_run).
	dryRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&mode=dry_run")
	if len(dryRows) != 1 {
		t.Fatalf("mode=dry_run for actor = %d rows, want 1", len(dryRows))
	}
	if m, _ := dryRows[0]["mode"].(string); m != "dry_run" {
		t.Fatalf("mode filter returned mode %q, want dry_run", m)
	}

	// Filter by status=200: actor's two reads are both 200.
	statusRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&status=200")
	if len(statusRows) != 2 {
		t.Fatalf("status=200 for actor = %d rows, want 2", len(statusRows))
	}
	for _, p := range statusRows {
		// JSON numbers decode to float64.
		if rs, _ := p["response_status"].(float64); int(rs) != 200 {
			t.Fatalf("status filter leaked a row with status %v", p["response_status"])
		}
	}

	// Filter by target (the audited request path). The actor's two reads
	// hit GET /auth/keys, so target=/auth/keys narrows to exactly those.
	targetRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&target=/auth/keys")
	if len(targetRows) != 2 {
		t.Fatalf("target=/auth/keys for actor = %d rows, want 2", len(targetRows))
	}
	for _, p := range targetRows {
		if rp, _ := p["request_path"].(string); rp != "/auth/keys" {
			t.Fatalf("target filter leaked a row with request_path %q", rp)
		}
	}
	// A target with no matching audit row narrows to zero.
	if none := auditRows(t, f, readerKey, "?key_name="+actorName+"&target=/no/such/path"); len(none) != 0 {
		t.Fatalf("target=/no/such/path = %d rows, want 0", len(none))
	}
}

// TestAuditRead_RotationFoundByActorFilter pins the key-lifecycle actor
// filter. A rotation row carries the new (surviving) key's
// key_id/key_name — like every other auth row — so an operator auditing
// a key by id surfaces the rotation that produced it. Before this, the
// rotation payload carried only old_key_id/new_key_id, so ?key_id= and
// ?key_name= silently dropped every rotation.
func TestAuditRead_RotationFoundByActorFilter(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Bootstrap an admin via anonymous mode.
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// A reader with audit:read.
	code, readerBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "audit-reader",
		"permissions": []map[string]any{{"action": "audit:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	// A key to rotate.
	const rotateeName = "rotatee"
	if code, body := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        rotateeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	}); code != 201 {
		t.Fatalf("mint rotatee: %d %+v", code, body)
	}

	// Rotate it. The rotation emits an auth.key_rotated row stamped with
	// key_id = new key / key_name = preserved name.
	code, rotBody := f.request(t, "POST", "/auth/keys/"+rotateeName+"/rotate", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("rotate %q: %d %+v", rotateeName, code, rotBody)
	}
	newKeyID, _ := rotBody["new_key_id"].(string)
	if newKeyID == "" {
		t.Fatalf("rotate response missing new_key_id: %+v", rotBody)
	}

	// findRotation pulls the key_rotated payload (if any) out of an
	// /audit result, matching on its new_key_id descriptive field.
	findRotation := func(rows []map[string]any) map[string]any {
		for _, p := range rows {
			if nk, _ := p["new_key_id"].(string); nk == newKeyID {
				return p
			}
		}
		return nil
	}

	// ?key_id=<new> must surface the rotation row, stamped with the new key.
	byID := findRotation(auditRows(t, f, readerKey, "?key_id="+newKeyID))
	if byID == nil {
		t.Fatalf("?key_id=%s did not surface the key_rotated row", newKeyID)
	}
	if kid, _ := byID["key_id"].(string); kid != newKeyID {
		t.Fatalf("rotation row key_id = %q, want new key id %q", kid, newKeyID)
	}

	// ?key_name=<name> must surface it too (the name is preserved).
	byName := findRotation(auditRows(t, f, readerKey, "?key_name="+rotateeName))
	if byName == nil {
		t.Fatalf("?key_name=%s did not surface the key_rotated row", rotateeName)
	}
	if kn, _ := byName["key_name"].(string); kn != rotateeName {
		t.Fatalf("rotation row key_name = %q, want %q", kn, rotateeName)
	}
}
