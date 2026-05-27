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
		if r.URL.Path != "/instances/abc/messages" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("kind") != "invalidate" {
			t.Errorf("kind filter missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{
					"id":          "m1",
					"instance_id": "abc",
					"kind":        "invalidate",
					"sender":      "operator",
					"sender_kind": "operator",
					"received_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListInstanceMessages(context.Background(), "abc", ListMessagesQuery{Kind: "invalidate"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].ID != "m1" {
		t.Errorf("messages: %+v", resp.Messages)
	}
}

func TestClient_GetMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages/m1" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "m1",
			"instance_id": "abc",
			"kind":        "invalidate",
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
}

func TestRunMessagesTail_RequiresInstance(t *testing.T) {
	if code := RunMessagesTail(context.Background(), []string{}); code != 2 {
		t.Errorf("exit code: %d", code)
	}
}
