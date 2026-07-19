// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"net/http"
	"testing"
)

func TestAuthCreateKey_DryRunInAnonymousModeNotesExitingAnonymousMode(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	status, body := h.httpPostAs(t, "/v1/auth/keys?dry_run=true", map[string]any{
		"name":        "first-key",
		"permissions": []map[string]any{{"action": "*"}},
	}, "", "")
	if status != http.StatusOK {
		t.Fatalf("dry-run create-key in anonymous mode: status=%d body=%v", status, body)
	}
	if body["dry_run"] != true {
		t.Fatalf("dry-run response must report dry_run=true: %v", body)
	}
	details, ok := body["would_have_created_key"].(map[string]any)
	if !ok {
		t.Fatalf("dry-run response must carry a would_have_created_key details object: %v", body)
	}
	note, _ := details["note"].(string)
	if note == "" {
		t.Fatalf("dry-run create-key while anonymous must carry details.note explaining that "+
			"committing this key exits anonymous mode; got details=%v", details)
	}
}

func TestAuthCreateKey_DryRunWithActiveKeyOmitsAnonymousNote(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})

	status, body := h.httpPostAs(t, "/v1/auth/keys?dry_run=true", map[string]any{
		"name":        "second-key",
		"permissions": []map[string]any{{"action": "*"}},
	}, adminKey, "")
	if status != http.StatusOK {
		t.Fatalf("dry-run create-key with an active key: status=%d body=%v", status, body)
	}
	details, ok := body["would_have_created_key"].(map[string]any)
	if !ok {
		t.Fatalf("dry-run response must carry a would_have_created_key details object: %v", body)
	}
	if _, present := details["note"]; present {
		t.Fatalf("dry-run create-key must not carry the anonymous-mode note once an active key already exists: %v", details)
	}
}
