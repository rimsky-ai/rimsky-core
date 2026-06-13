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
	"time"
)

// auditRows runs GET /audit with the given query string under key and
// returns the decoded `audit` array (one map per row).
func auditRows(t *testing.T, f *authFixture, key, query string) []map[string]any {
	t.Helper()
	code, body := f.request(t, "GET", "/v1/audit"+query, key, nil)
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
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
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
	code, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        readerName,
		"permissions": []map[string]any{{"action": "audit:read"}, {"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	const actorName = "audit-actor"
	code, actorBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
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
	if st, _ := f.request(t, "GET", "/v1/auth/keys", actorKey, nil); st != 200 {
		t.Fatalf("actor execute read: %d", st)
	}
	if st, _ := f.request(t, "GET", "/v1/auth/keys?dry_run=true", actorKey, nil); st != 200 {
		t.Fatalf("actor dry-run read: %d", st)
	}

	// A key WITHOUT audit:read is denied (403). The actor key has only
	// auth:read.
	if st, body := f.request(t, "GET", "/v1/audit", actorKey, nil); st != 403 {
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
	// hit GET /v1/auth/keys, so target=/v1/auth/keys narrows to exactly
	// those.
	targetRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&target=/v1/auth/keys")
	if len(targetRows) != 2 {
		t.Fatalf("target=/v1/auth/keys for actor = %d rows, want 2", len(targetRows))
	}
	for _, p := range targetRows {
		if rp, _ := p["request_path"].(string); rp != "/v1/auth/keys" {
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
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// A reader with audit:read.
	code, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "audit-reader",
		"permissions": []map[string]any{{"action": "audit:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	// A key to rotate.
	const rotateeName = "rotatee"
	if code, body := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        rotateeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	}); code != 201 {
		t.Fatalf("mint rotatee: %d %+v", code, body)
	}

	// Rotate it. The rotation emits an auth.key_rotated row stamped with
	// key_id = new key / key_name = preserved name.
	code, rotBody := f.request(t, "POST", "/v1/auth/keys/"+rotateeName+"/rotate", adminKey, map[string]any{})
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

// TestAuditRead_StoryAuditLogReadAcceptance is the executable proof for
// STORY-audit-log-read. The spec's Acceptance is:
//
//	Through GET /audit (gated by audit:read), after an admin mints /
//	revokes / rotates keys and a non-admin caller triggers an access
//	denied, the audit log returns each event in timestamp order
//	carrying actor identity, action name, outcome, and resource target.
//
// The spec's Falsifier is: a real access denied doesn't appear in the
// audit, OR dry-run-mode attempts are absent, OR actor identity is
// dropped from the record.
//
// This test drives the full mint / revoke / rotate / deny + dry-run
// sequence through the real control-api and asserts every payload
// surfaces actor identity / action name / outcome / resource target,
// the access-denied row appears, the dry-run attempt appears, and the
// audit feed is timestamp-ordered.
//
// Falsifier guards are inline; each property has a fatal assertion
// pointing at the spec clause it protects.
func TestAuditRead_StoryAuditLogReadAcceptance(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Drive the audit-fixture clock forward by a millisecond between
	// each emission so wire-decoded payloads (and the cross-kind
	// timestamp order this test verifies) are not tied via wall-clock
	// resolution. The ControllableClock backs the AuthState.Clock the
	// emit helpers stamp duration_ms / occurred_at from.
	tick := func() { f.clock.Advance(time.Millisecond) }

	// --- Admin bootstrap (anonymous → authenticated). The POST itself
	// emits one auth.access_attempted row (mode:execute, action:auth:create).
	tick()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}
	adminKeyID, _ := adminBody["id"].(string)
	if adminKeyID == "" {
		t.Fatalf("admin id missing: %+v", adminBody)
	}

	// --- Audit reader: has audit:read so it can read /audit.
	const readerName = "audit-reader"
	tick()
	code, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        readerName,
		"permissions": []map[string]any{{"action": "audit:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	// --- Mint a "subject" key the admin will revoke (forcing an
	// auth.key_revoked row stamped with the subject's key_id/key_name).
	const revokeeName = "subject-revokee"
	tick()
	code, revBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        revokeeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint revokee: %d %+v", code, revBody)
	}
	revokeeKeyID, _ := revBody["id"].(string)
	if revokeeKeyID == "" {
		t.Fatalf("revokee id missing: %+v", revBody)
	}

	// --- Mint a "subject" key the admin will rotate (the rotation
	// row's actor is the new (surviving) key id; the spec's audit
	// reader filters on it).
	const rotateeName = "subject-rotatee"
	tick()
	code, rotateeBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        rotateeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint rotatee: %d %+v", code, rotateeBody)
	}

	// --- Mint a non-admin actor we'll use to trigger:
	//       (a) a dry-run-mode read (mode:dry_run, executed:true)
	//       (b) a denied write attempt (auth.access_denied row)
	const actorName = "subject-actor"
	tick()
	code, actorBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        actorName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint actor: %d %+v", code, actorBody)
	}
	actorKey, _ := actorBody["plaintext"].(string)
	actorKeyID, _ := actorBody["id"].(string)
	if actorKeyID == "" {
		t.Fatalf("actor id missing: %+v", actorBody)
	}

	// --- Trigger the dry-run-mode attempt under the actor. A read
	// genuinely runs even under dry-run, so this lands an
	// auth.access_attempted row stamped mode:dry_run, executed:true.
	tick()
	if st, _ := f.request(t, "GET", "/v1/auth/keys?dry_run=true", actorKey, nil); st != 200 {
		t.Fatalf("actor dry-run read: %d", st)
	}

	// --- Trigger an access denied: the actor has no auth:create
	// permission, so POST /v1/auth/keys lands an auth.access_denied row
	// (denial_reason:permission_denied) with the actor's identity.
	tick()
	if st, body := f.request(t, "POST", "/v1/auth/keys", actorKey, map[string]any{
		"name":        "should-not-exist",
		"permissions": []map[string]any{{"action": "auth:read"}},
	}); st != 403 {
		t.Fatalf("actor without auth:create should be 403, got %d %+v", st, body)
	}

	// --- Revoke the revokee. Emits auth.key_revoked with subject
	// key_id/key_name + revoked_by_key_id = admin.
	tick()
	if st, body := f.request(t, "DELETE", "/v1/auth/keys/"+revokeeName, adminKey, nil); st != 204 && st != 200 {
		t.Fatalf("revoke %q: %d %+v", revokeeName, st, body)
	}

	// --- Rotate the rotatee. Emits auth.key_rotated stamped with the
	// new (surviving) key id under the uniform actor key_id/key_name
	// fields.
	tick()
	code, rotBody := f.request(t, "POST", "/v1/auth/keys/"+rotateeName+"/rotate", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("rotate %q: %d %+v", rotateeName, code, rotBody)
	}
	newRotateeKeyID, _ := rotBody["new_key_id"].(string)
	if newRotateeKeyID == "" {
		t.Fatalf("rotate response missing new_key_id: %+v", rotBody)
	}

	// ------------------------------------------------------------------
	// Acceptance assertions
	// ------------------------------------------------------------------

	// Falsifier guard 1: actor identity must NOT be dropped from any
	// auth row. Page through every kind and assert each carries a
	// key_id (or, where the spec allows nullable identity — only on
	// pre-action-resolution denial rows whose denial_reason is
	// no_token / invalid_token / expired_token / revoked_token — both
	// nil). For permission_denied and every other kind the identity is
	// populated.
	rows := auditRowsWithKind(t, f, readerKey, "")
	if len(rows) == 0 {
		t.Fatalf("audit feed empty — expected mint + revoke + rotate + access_denied + dry-run rows")
	}
	for _, e := range rows {
		kind, _ := e["kind"].(string)
		payload, _ := e["payload"].(map[string]any)
		kid, _ := payload["key_id"].(string)
		switch kind {
		case "auth.access_denied":
			// permission_denied retains identity. Pre-action denials
			// (no_token / invalid_token / expired_token / revoked_token)
			// are allowed to drop it.
			dr, _ := payload["denial_reason"].(string)
			if dr == "permission_denied" && kid == "" {
				t.Fatalf("permission_denied row dropped actor key_id: %+v", payload)
			}
		case "auth.access_attempted":
			// Anonymous-mode bootstrap (no Bearer) is the documented
			// exception: identity_kind:anonymous with no key_id is the
			// expected payload for the first-ever mint. Every
			// authenticated attempt must carry key_id — that's the
			// Falsifier this guard pins.
			ik, _ := payload["identity_kind"].(string)
			if ik == "anonymous" {
				continue
			}
			if kid == "" {
				t.Fatalf("%s row dropped actor key_id (Falsifier: actor identity dropped from record): %+v", kind, payload)
			}
		default:
			if kid == "" {
				t.Fatalf("%s row dropped actor key_id (Falsifier: actor identity dropped from record): %+v", kind, payload)
			}
		}
	}

	// Acceptance: actor identity / action name / outcome / resource target.
	// For each emitted kind, pull the row and assert each clause's field.

	// 1a. auth.key_created — find the row stamped with revokeeKeyID and
	//     check actor identity + permissions (action grant set) +
	//     created_by_key_id (admin).
	created := findRowMatching(rows, "auth.key_created", func(p map[string]any) bool {
		kid, _ := p["key_id"].(string)
		return kid == revokeeKeyID
	})
	if created == nil {
		t.Fatalf("auth.key_created row for revokee missing — Acceptance: admin mint must appear in audit")
	}
	if kn, _ := created["key_name"].(string); kn != revokeeName {
		t.Fatalf("auth.key_created row missing actor key_name: got %q want %q", kn, revokeeName)
	}
	if cb, _ := created["created_by_key_id"].(string); cb != adminKeyID {
		t.Fatalf("auth.key_created row missing created_by_key_id (admin actor): got %q want %q", cb, adminKeyID)
	}
	if _, ok := created["permissions"]; !ok {
		t.Fatalf("auth.key_created row missing permissions (action-grant payload)")
	}

	// 1b. auth.key_revoked — find by key_id; check actor identity +
	//     revoked_by_key_id (admin).
	revoked := findRowMatching(rows, "auth.key_revoked", func(p map[string]any) bool {
		kid, _ := p["key_id"].(string)
		return kid == revokeeKeyID
	})
	if revoked == nil {
		t.Fatalf("auth.key_revoked row missing for revokee — Acceptance: admin revoke must appear")
	}
	if kn, _ := revoked["key_name"].(string); kn != revokeeName {
		t.Fatalf("auth.key_revoked row missing actor key_name: got %q want %q", kn, revokeeName)
	}
	if rb, _ := revoked["revoked_by_key_id"].(string); rb != adminKeyID {
		t.Fatalf("auth.key_revoked row missing revoked_by_key_id (admin actor): got %q want %q", rb, adminKeyID)
	}
	if rs, _ := revoked["reason"].(string); rs == "" {
		t.Fatalf("auth.key_revoked row missing reason")
	}

	// 1c. auth.key_rotated — find by new_key_id; check actor key_id is
	//     the new surviving key + name carried through.
	rotated := findRowMatching(rows, "auth.key_rotated", func(p map[string]any) bool {
		nk, _ := p["new_key_id"].(string)
		return nk == newRotateeKeyID
	})
	if rotated == nil {
		t.Fatalf("auth.key_rotated row missing — Acceptance: admin rotate must appear")
	}
	if kid, _ := rotated["key_id"].(string); kid != newRotateeKeyID {
		t.Fatalf("auth.key_rotated row actor key_id mismatch: got %q want new %q", kid, newRotateeKeyID)
	}
	if kn, _ := rotated["key_name"].(string); kn != rotateeName {
		t.Fatalf("auth.key_rotated row actor key_name dropped: got %q want %q", kn, rotateeName)
	}

	// 1d. auth.access_denied — find the actor's denied POST. Check
	//     identity + action name + outcome (response_status:403,
	//     executed:false) + resource target (request_path).
	//
	// Falsifier guard: a real access denied must appear in the audit.
	denied := findRowMatching(rows, "auth.access_denied", func(p map[string]any) bool {
		kid, _ := p["key_id"].(string)
		rp, _ := p["request_path"].(string)
		return kid == actorKeyID && rp == "/v1/auth/keys"
	})
	if denied == nil {
		t.Fatalf("auth.access_denied row for actor POST /v1/auth/keys missing — Falsifier: a real access denied doesn't appear in the audit")
	}
	if a, _ := denied["action"].(string); a != "auth:create" {
		t.Fatalf("access_denied row missing action name: got %q want %q", a, "auth:create")
	}
	if rs, _ := denied["response_status"].(float64); int(rs) != 403 {
		t.Fatalf("access_denied row missing outcome: got %v want 403", denied["response_status"])
	}
	if ex, _ := denied["executed"].(bool); ex {
		t.Fatalf("access_denied row outcome wrong: executed should be false")
	}
	if dr, _ := denied["denial_reason"].(string); dr != "permission_denied" {
		t.Fatalf("access_denied row denial_reason wrong: got %q want permission_denied", dr)
	}
	if rm, _ := denied["request_method"].(string); rm != "POST" {
		t.Fatalf("access_denied row resource target wrong (method): got %q want POST", rm)
	}

	// 1e. dry-run-mode attempt — Falsifier guard: dry-run-mode attempts
	//     must NOT be absent from the audit.
	dryRun := findRowMatching(rows, "auth.access_attempted", func(p map[string]any) bool {
		kid, _ := p["key_id"].(string)
		mode, _ := p["mode"].(string)
		rp, _ := p["request_path"].(string)
		return kid == actorKeyID && mode == "dry_run" && rp == "/v1/auth/keys"
	})
	if dryRun == nil {
		t.Fatalf("auth.access_attempted dry-run row for actor missing — Falsifier: dry-run-mode attempts are absent")
	}
	if a, _ := dryRun["action"].(string); a != "auth:read" {
		t.Fatalf("dry_run row missing action name: got %q want auth:read", a)
	}
	if rs, _ := dryRun["response_status"].(float64); int(rs) != 200 {
		t.Fatalf("dry_run row missing outcome: got %v want 200", dryRun["response_status"])
	}
	if rm, _ := dryRun["request_method"].(string); rm != "GET" {
		t.Fatalf("dry_run row resource target wrong (method): got %q want GET", rm)
	}

	// 1f. Acceptance: GET /v1/audit returns each event in timestamp
	//     order. Page-default order is recent-first (descending
	//     occurred_at) — the falsifier is rows interleaved out of time
	//     (e.g., grouped by source kind). Drive the clock forward
	//     between every emit above so occurred_at is strictly distinct
	//     per row, then assert the feed is monotonically descending.
	//     The mix of kinds in the feed (access_attempted +
	//     access_denied + key_created + key_revoked + key_rotated)
	//     means a source-grouped feed would fail this assertion: the
	//     key_rotated row (last in time) would have to lead the feed,
	//     not appear interleaved with other kinds by timestamp.
	// Timestamps must be PARSED, not compared as strings: the wire
	// shape is RFC3339Nano with trailing zeros trimmed, and trimmed
	// fractions do not order lexically ("…0.12Z" > "…0.123Z" because
	// 'Z' > '3', yet 0.12s < 0.123s) — a string comparison here flakes
	// whenever two adjacent rows land on fractional seconds with
	// different trimmed widths.
	var prev time.Time
	var prevRaw string
	sawCrossKind := false
	var prevKind string
	for i, e := range rows {
		raw, _ := e["occurred_at"].(string)
		if raw == "" {
			t.Fatalf("row %d missing occurred_at — Acceptance: events returned in timestamp order", i)
		}
		ts, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			t.Fatalf("row %d occurred_at %q not RFC3339: %v", i, raw, err)
		}
		k, _ := e["kind"].(string)
		if i > 0 {
			if ts.After(prev) {
				t.Fatalf("row %d (%s/%s) breaks descending timestamp order vs prior (%s/%s) — Falsifier: source-grouped rather than timestamp-ordered", i, k, raw, prevKind, prevRaw)
			}
			if k != prevKind {
				sawCrossKind = true
			}
		}
		prev = ts
		prevRaw = raw
		prevKind = k
	}
	// The Acceptance only holds nontrivially when distinct kinds
	// interleave: the falsifier is a source-grouped feed, which would
	// keep rows of the same kind contiguous. The above sequence emits
	// at least five distinct kinds, so we expect a cross-kind step.
	if !sawCrossKind {
		t.Fatalf("audit feed never crossed kinds — cannot assert cross-kind chronological order: %d rows", len(rows))
	}
}

// auditRowsWithKind is auditRows that surfaces the row envelope (kind
// + occurred_at + payload), not just the payload, so callers can verify
// timestamp ordering and kind-tagged behavior.
func auditRowsWithKind(t *testing.T, f *authFixture, key, query string) []map[string]any {
	t.Helper()
	code, body := f.request(t, "GET", "/v1/audit"+query, key, nil)
	if code != 200 {
		t.Fatalf("GET /audit%s: %d %+v", query, code, body)
	}
	raw, _ := body["audit"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// findRowMatching returns the payload of the first row whose `kind`
// matches and whose payload satisfies the predicate, or nil if none.
func findRowMatching(rows []map[string]any, kind string, match func(map[string]any) bool) map[string]any {
	for _, e := range rows {
		k, _ := e["kind"].(string)
		if k != kind {
			continue
		}
		p, _ := e["payload"].(map[string]any)
		if p == nil {
			continue
		}
		if match(p) {
			return p
		}
	}
	return nil
}
