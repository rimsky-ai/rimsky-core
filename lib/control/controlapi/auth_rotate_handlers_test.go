// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func (h *senderSubjectHarness) httpPostRaw(t *testing.T, path, bearer, rawBody string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, bytes.NewReader([]byte(rawBody)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	out := map[string]any{}
	if len(rawResp) > 0 {
		_ = json.Unmarshal(rawResp, &out)
	}
	return resp.StatusCode, out
}

func TestAuthRotateKey_MalformedJSONBodyRejected400(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})
	_, targetID := h.mintActiveAPIKey(t, "target", []map[string]any{{"action": "instance:read"}})

	status, body := h.httpPostRaw(t, "/v1/auth/keys/"+targetID.String()+"/rotate", adminKey, `{"grace":`)
	if status != http.StatusBadRequest {
		t.Fatalf("rotating with a malformed JSON body: got status=%d body=%v want 400 "+
			"(auth_handlers.go::handleRotateKey must reject invalid JSON like handleCreateKey does, not silently apply the default grace)", status, body)
	}

	row, ok, err := h.db.Tables().APIKeys().GetByID(context.Background(), targetID, nil)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !ok {
		t.Fatalf("target key vanished")
	}
	if row.RevokeAt != nil {
		t.Fatalf("malformed-body rotation must not have taken effect, but old row's RevokeAt = %v", row.RevokeAt)
	}
}

func TestAuthRotateKey_NonPositiveGraceRejected400(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})
	_, targetID := h.mintActiveAPIKey(t, "target", []map[string]any{{"action": "instance:read"}})

	for _, grace := range []string{"-24h", "0s"} {
		status, body := h.httpPostAs(t, "/v1/auth/keys/"+targetID.String()+"/rotate", map[string]any{
			"grace": grace,
		}, adminKey, "")
		if status != http.StatusBadRequest {
			t.Fatalf("rotating with grace=%q: got status=%d body=%v want 400 "+
				"(non-positive grace must not silently produce an immediate/backdated revoke_at)", grace, status, body)
		}
	}

	row, ok, err := h.db.Tables().APIKeys().GetByID(context.Background(), targetID, nil)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !ok {
		t.Fatalf("target key vanished")
	}
	if row.RevokeAt != nil {
		t.Fatalf("rejected rotation must not have taken effect, but old row's RevokeAt = %v", row.RevokeAt)
	}
}

func TestAuthRotateKey_RecordsActorProvenance(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, adminID := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})
	_, targetID := h.mintActiveAPIKey(t, "target", []map[string]any{{"action": "instance:read"}})

	status, body := h.httpPostAs(t, "/v1/auth/keys/"+targetID.String()+"/rotate", map[string]any{}, adminKey, "")
	if status != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%v want 200", status, body)
	}
	newKeyID, _ := body["new_key_id"].(string)
	if newKeyID == "" {
		t.Fatalf("rotate response missing new_key_id: %v", body)
	}

	row, ok, err := h.db.Tables().APIKeys().GetByName(context.Background(), "target", nil)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if !ok {
		t.Fatalf("rotated row not found by name")
	}
	if row.CreatedByKeyID == nil || row.CreatedByKeyID.String() != adminID.String() {
		t.Fatalf("rotated row's CreatedByKeyID = %v, want the rotating admin key %s "+
			"(auth_handlers.go::handleRotateKey must stamp actor provenance on the new row like handleCreateKey does)",
			row.CreatedByKeyID, adminID)
	}

	auditStatus, auditBody := h.httpPostAsGet(t, "/v1/audit?kind="+auth.EventKeyRotated, adminKey)
	if auditStatus != http.StatusOK {
		t.Fatalf("audit list: status=%d body=%v", auditStatus, auditBody)
	}
	rows, _ := auditBody["audit"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected exactly one auth.key_rotated audit row, got %d: %v", len(rows), auditBody)
	}
	entry, _ := rows[0].(map[string]any)
	payload, _ := entry["payload"].(map[string]any)
	rotatedBy, _ := payload["rotated_by_key_id"].(string)
	if rotatedBy != adminID.String() {
		t.Fatalf("auth.key_rotated payload rotated_by_key_id = %q, want %q (payload: %v)", rotatedBy, adminID.String(), payload)
	}
}

func TestAuthRotateKey_AlreadyInGraceRejected409(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})
	_, targetID := h.mintActiveAPIKey(t, "target", []map[string]any{{"action": "instance:read"}})

	status, body := h.httpPostAs(t, "/v1/auth/keys/"+targetID.String()+"/rotate", map[string]any{}, adminKey, "")
	if status != http.StatusOK {
		t.Fatalf("first rotate: status=%d body=%v want 200", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/auth/keys/"+targetID.String()+"/rotate", map[string]any{}, adminKey, "")
	if status != http.StatusConflict {
		t.Fatalf("re-rotating a key already in rotation grace (addressed by ID): got status=%d body=%v want 409 "+
			"(previously this fell through to an unmapped persistence.ErrAPIKeyNameTaken -> 500)", status, body)
	}
}

func (h *senderSubjectHarness) httpPostAsGet(t *testing.T, path, bearer string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	out := map[string]any{}
	if len(rawResp) > 0 {
		_ = json.Unmarshal(rawResp, &out)
	}
	return resp.StatusCode, out
}
