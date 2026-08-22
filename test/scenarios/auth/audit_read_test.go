// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log
// @concept: permission

package auth_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

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

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

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

	if st, _ := f.request(t, "GET", "/v1/auth/keys", actorKey, nil); st != 200 {
		t.Fatalf("actor execute read: %d", st)
	}
	if st, _ := f.request(t, "GET", "/v1/auth/keys?dry_run=true", actorKey, nil); st != 200 {
		t.Fatalf("actor dry-run read: %d", st)
	}

	if st, body := f.request(t, "GET", "/v1/audit", actorKey, nil); st != 403 {
		t.Fatalf("actor without audit:read should be 403, got %d %+v", st, body)
	}

	got := auditRows(t, f, readerKey, "?key_name="+actorName)
	if len(got) == 0 {
		t.Fatalf("key_name filter returned no rows for actor %q", actorName)
	}
	for _, p := range got {
		if kn, _ := p["key_name"].(string); kn != actorName {
			t.Fatalf("key_name filter leaked a row for %q", kn)
		}
	}

	got = auditRows(t, f, readerKey, "?action=auth:read")
	if len(got) == 0 {
		t.Fatalf("action=auth:read returned no rows")
	}
	for _, p := range got {
		if a, _ := p["action"].(string); a != "auth:read" {
			t.Fatalf("action filter leaked a row for action %q", a)
		}
	}

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

	dryRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&mode=dry_run")
	if len(dryRows) != 1 {
		t.Fatalf("mode=dry_run for actor = %d rows, want 1", len(dryRows))
	}
	if m, _ := dryRows[0]["mode"].(string); m != "dry_run" {
		t.Fatalf("mode filter returned mode %q, want dry_run", m)
	}

	statusRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&status=200")
	if len(statusRows) != 2 {
		t.Fatalf("status=200 for actor = %d rows, want 2", len(statusRows))
	}
	for _, p := range statusRows {
		if rs, _ := p["response_status"].(float64); int(rs) != 200 {
			t.Fatalf("status filter leaked a row with status %v", p["response_status"])
		}
	}

	targetRows := auditRows(t, f, readerKey, "?key_name="+actorName+"&target=/v1/auth/keys")
	if len(targetRows) != 2 {
		t.Fatalf("target=/v1/auth/keys for actor = %d rows, want 2", len(targetRows))
	}
	for _, p := range targetRows {
		if rp, _ := p["request_path"].(string); rp != "/v1/auth/keys" {
			t.Fatalf("target filter leaked a row with request_path %q", rp)
		}
	}
	if none := auditRows(t, f, readerKey, "?key_name="+actorName+"&target=/no/such/path"); len(none) != 0 {
		t.Fatalf("target=/no/such/path = %d rows, want 0", len(none))
	}
}

func TestAuditRead_RotationFoundByActorFilter(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	code, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "audit-reader",
		"permissions": []map[string]any{{"action": "audit:read"}},
	})
	if code != 201 {
		t.Fatalf("mint reader: %d %+v", code, readerBody)
	}
	readerKey, _ := readerBody["plaintext"].(string)

	const rotateeName = "rotatee"
	if code, body := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        rotateeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	}); code != 201 {
		t.Fatalf("mint rotatee: %d %+v", code, body)
	}

	code, rotBody := f.request(t, "POST", "/v1/auth/keys/"+rotateeName+"/rotate", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("rotate %q: %d %+v", rotateeName, code, rotBody)
	}
	newKeyID, _ := rotBody["new_key_id"].(string)
	if newKeyID == "" {
		t.Fatalf("rotate response missing new_key_id: %+v", rotBody)
	}

	findRotation := func(rows []map[string]any) map[string]any {
		for _, p := range rows {
			if nk, _ := p["new_key_id"].(string); nk == newKeyID {
				return p
			}
		}
		return nil
	}

	byID := findRotation(auditRows(t, f, readerKey, "?key_id="+newKeyID))
	if byID == nil {
		t.Fatalf("?key_id=%s did not surface the key_rotated row", newKeyID)
	}
	if kid, _ := byID["key_id"].(string); kid != newKeyID {
		t.Fatalf("rotation row key_id = %q, want new key id %q", kid, newKeyID)
	}

	byName := findRotation(auditRows(t, f, readerKey, "?key_name="+rotateeName))
	if byName == nil {
		t.Fatalf("?key_name=%s did not surface the key_rotated row", rotateeName)
	}
	if kn, _ := byName["key_name"].(string); kn != rotateeName {
		t.Fatalf("rotation row key_name = %q, want %q", kn, rotateeName)
	}
}

func TestAuditRead_StoryAuditLogReadAcceptance(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	tick := func() { f.clock.Advance(time.Millisecond) }

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

	const rotateeName = "subject-rotatee"
	tick()
	code, rotateeBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        rotateeName,
		"permissions": []map[string]any{{"action": "auth:read"}},
	})
	if code != 201 {
		t.Fatalf("mint rotatee: %d %+v", code, rotateeBody)
	}

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

	tick()
	if st, _ := f.request(t, "GET", "/v1/auth/keys?dry_run=true", actorKey, nil); st != 200 {
		t.Fatalf("actor dry-run read: %d", st)
	}

	tick()
	if st, body := f.request(t, "POST", "/v1/auth/keys", actorKey, map[string]any{
		"name":        "should-not-exist",
		"permissions": []map[string]any{{"action": "auth:read"}},
	}); st != 403 {
		t.Fatalf("actor without auth:create should be 403, got %d %+v", st, body)
	}

	tick()
	if st, body := f.request(t, "DELETE", "/v1/auth/keys/"+revokeeName, adminKey, nil); st != 204 && st != 200 {
		t.Fatalf("revoke %q: %d %+v", revokeeName, st, body)
	}

	tick()
	code, rotBody := f.request(t, "POST", "/v1/auth/keys/"+rotateeName+"/rotate", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("rotate %q: %d %+v", rotateeName, code, rotBody)
	}
	newRotateeKeyID, _ := rotBody["new_key_id"].(string)
	if newRotateeKeyID == "" {
		t.Fatalf("rotate response missing new_key_id: %+v", rotBody)
	}

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
			dr, _ := payload["denial_reason"].(string)
			if dr == "permission_denied" && kid == "" {
				t.Fatalf("permission_denied row dropped actor key_id: %+v", payload)
			}
		case "auth.access_attempted":
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
	if !sawCrossKind {
		t.Fatalf("audit feed never crossed kinds — cannot assert cross-kind chronological order: %d rows", len(rows))
	}
}

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

func allAuditPayloads(t *testing.T, f *authFixture) []map[string]any {
	t.Helper()
	ctx := context.Background()
	kinds := []string{
		auth.EventAccessAttempted.String(), auth.EventAccessDenied.String(),
		auth.EventKeyCreated.String(), auth.EventKeyRevoked.String(), auth.EventKeyRotated.String(),
	}
	var out []map[string]any
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, k := range kinds {
			rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{KindIn: []string{k}}, persistence.ListPagination{Limit: 500}, tx)
			if err != nil {
				return err
			}
			for _, e := range rl.Events {
				out = append(out, e.Payload.Map())
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("collect audit payloads: %v", err)
	}
	return out
}

func TestAuditContent_NoBearerSecretLeaks(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	if st, _ := f.request(t, "GET", "/v1/auth/keys", adminKey, nil); st != 200 {
		t.Fatalf("admin read with bearer: %d", st)
	}

	_, narrowBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "narrow",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey, _ := narrowBody["plaintext"].(string)
	if narrowKey == "" {
		t.Fatalf("mint narrow: %+v", narrowBody)
	}

	_, rotBody := f.request(t, "POST", "/v1/auth/keys/narrow/rotate", adminKey, map[string]any{})
	rotatedKey, _ := rotBody["plaintext"].(string)
	if rotatedKey == "" {
		t.Fatalf("rotate narrow: %+v", rotBody)
	}

	if st, _ := f.request(t, "POST", "/v1/auth/keys", rotatedKey, map[string]any{
		"name":        "should-not-exist",
		"permissions": []map[string]any{{"action": "instance:read"}},
	}); st != 403 {
		t.Fatalf("narrow-key create should be 403 (drives an access_denied audit row carrying the bearer identity): %d", st)
	}

	if st, _ := f.request(t, "DELETE", "/v1/auth/keys/narrow", adminKey, nil); st != 200 && st != 204 {
		t.Fatalf("revoke narrow: %d", st)
	}

	const bogusBearer = "rk_bogus_secret_that_must_never_be_persisted_0123456789abcdef"
	_, _ = f.request(t, "GET", "/v1/auth/keys", bogusBearer, nil)

	secrets := []string{adminKey, narrowKey, rotatedKey, bogusBearer}

	payloads := allAuditPayloads(t, f)
	if len(payloads) == 0 {
		t.Fatalf("no audit payloads collected — expected mint/rotate/revoke/denied/attempted rows")
	}
	for _, p := range payloads {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		s := string(raw)
		for _, secret := range secrets {
			if strings.Contains(s, secret) {
				t.Fatalf("audit payload leaks a bearer plaintext secret (a persisted audit record must never contain the token): payload=%s", s)
			}
			if strings.Contains(s, "Bearer "+secret) {
				t.Fatalf("audit payload leaks the Authorization header value: payload=%s", s)
			}
		}
	}
}

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
