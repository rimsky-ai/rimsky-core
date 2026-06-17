// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_ListInstanceMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/abc/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "ping/recheck" {
			t.Errorf("type filter missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{
					"id":          "m1",
					"instance_id": "abc",
					"type":        "ping/recheck",
					"sender":      "operator",
					"sender_kind": "operator",
					"received_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListInstanceMessages(context.Background(), "abc", ListMessagesQuery{Type: "ping/recheck"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].ID != "m1" {
		t.Errorf("messages: %+v", resp.Messages)
	}
	if resp.Messages[0].Type != "ping/recheck" {
		t.Errorf("decoded type: got %q, want %q", resp.Messages[0].Type, "ping/recheck")
	}
}

func TestClient_GetMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/m1" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "m1",
			"instance_id": "abc",
			"type":        "ping/recheck",
			"sender":      "operator",
			"sender_kind": "operator",
			"received_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	m, err := c.GetMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m1" {
		t.Errorf("id: %s", m.ID)
	}
	if m.Type != "ping/recheck" {
		t.Errorf("decoded type: got %q, want %q", m.Type, "ping/recheck")
	}
}

func TestRunMessagesTail_RequiresInstance(t *testing.T) {
	if code := RunMessagesTail(context.Background(), []string{}); code != 2 {
		t.Errorf("exit code: %d", code)
	}
}

func TestClient_CreateInstanceMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/v1/instances/abc/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "test-key" {
			t.Errorf("Idempotency-Key: got %q, want %q", got, "test-key")
		}
		var body struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Type != "" {
			t.Errorf("type: got %q, want empty", body.Type)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"message_id": "m1"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.CreateInstanceMessage(context.Background(), "abc", "test-key", CreateInstanceMessageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.MessageID != "m1" {
		t.Errorf("MessageID: %s", resp.MessageID)
	}
}
