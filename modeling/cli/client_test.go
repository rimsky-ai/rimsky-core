// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/modeling/node"
)

// helperServer stands up a one-shot httptest server that asserts the
// inbound request matches expectMethod+expectPath, optionally checks the
// JSON body, and returns the canned response.
func helperServer(t *testing.T, expectMethod, expectPath string, expectBody map[string]any, status int, respBody any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != expectMethod {
			t.Errorf("method: got %s want %s", r.Method, expectMethod)
		}
		if r.URL.Path != expectPath {
			t.Errorf("path: got %s want %s", r.URL.Path, expectPath)
		}
		if expectBody != nil {
			raw, _ := io.ReadAll(r.Body)
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Errorf("decode req body: %v", err)
			}
			for k, v := range expectBody {
				if !equalJSON(got[k], v) {
					t.Errorf("body[%q]: got %v want %v", k, got[k], v)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if respBody != nil {
			_ = json.NewEncoder(w).Encode(respBody)
		}
	}))
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestClient_RegisterTemplate(t *testing.T) {
	srv := helperServer(t, http.MethodPost, "/templates",
		map[string]any{"tag": "ingest@1.0"},
		http.StatusCreated,
		map[string]any{"template_id": "sha256-abc", "tags": []string{"ingest@1.0"}},
	)
	defer srv.Close()

	c := NewClient(srv.URL)
	got, err := c.RegisterTemplate(context.Background(), RegisterTemplateRequest{
		Spec: node.TemplateSpec{Name: "x", Version: "1"},
		Tag:  "ingest@1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != "sha256-abc" {
		t.Errorf("hash: %q", got.Hash())
	}
}

func TestClient_ListTemplates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "deployed" {
			t.Errorf("state filter missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"templates": []map[string]any{
				{"id": "sha256-abc", "state": "deployed"},
			},
			"next_cursor": "next",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	got, err := c.ListTemplates(context.Background(), ListTemplatesQuery{State: "deployed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Templates) != 1 || got.Templates[0].ID != "sha256-abc" {
		t.Errorf("templates: %+v", got.Templates)
	}
	if got.NextCursor != "next" {
		t.Errorf("next_cursor: %q", got.NextCursor)
	}
}

func TestClient_GetTemplate_NotFound(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/templates/missing",
		nil, http.StatusNotFound,
		map[string]any{"error": "template not found"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	_, err := c.GetTemplate(context.Background(), "missing")
	if err == nil {
		t.Fatal("want error")
	}
	if !IsNotFound(err) {
		t.Errorf("want IsNotFound, got %v", err)
	}
}

func TestClient_DeployTemplate(t *testing.T) {
	srv := helperServer(t, http.MethodPost, "/templates/foo/deploy",
		nil, http.StatusOK,
		map[string]any{"state": "deployed"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.DeployTemplate(context.Background(), "foo"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_DeleteTemplate_Conflict(t *testing.T) {
	srv := helperServer(t, http.MethodDelete, "/templates/foo",
		nil, http.StatusConflict,
		map[string]any{"error": "template has active instances"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	err := c.DeleteTemplate(context.Background(), "foo")
	if !IsConflict(err) {
		t.Errorf("want IsConflict, got %v", err)
	}
}

func TestClient_CreateTag(t *testing.T) {
	srv := helperServer(t, http.MethodPost, "/tags",
		map[string]any{"tag": "foo", "template": "sha256-abc"},
		http.StatusCreated,
		map[string]any{"tag": "foo", "template_id": "sha256-abc"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.CreateTag(context.Background(), CreateTagRequest{Tag: "foo", Template: "sha256-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "foo" || got.TemplateID != "sha256-abc" {
		t.Errorf("got %+v", got)
	}
}

func TestClient_ListTags(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/tags", nil, http.StatusOK,
		map[string]any{
			"tags": []map[string]any{{"tag": "a", "template_id": "h1"}},
		},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.ListTags(context.Background(), ListTagsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0].Tag != "a" {
		t.Errorf("got %+v", got.Tags)
	}
}

func TestClient_MoveTag(t *testing.T) {
	srv := helperServer(t, http.MethodPut, "/tags/foo",
		map[string]any{"template": "sha256-bcd"},
		http.StatusOK,
		map[string]any{"tag": "foo", "template_id": "sha256-bcd"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.MoveTag(context.Background(), "foo", MoveTagRequest{Template: "sha256-bcd"}); err != nil {
		t.Fatal(err)
	}
}

func TestClient_DeleteTag(t *testing.T) {
	srv := helperServer(t, http.MethodDelete, "/tags/foo",
		nil, http.StatusOK,
		map[string]any{"deleted": true},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.DeleteTag(context.Background(), "foo"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_CreateInstance(t *testing.T) {
	key := "compose:p:n"
	srv := helperServer(t, http.MethodPost, "/instances",
		map[string]any{"template": "sha256-abc", "instance_key": key},
		http.StatusCreated,
		map[string]any{"instance_id": "uuid-x", "template_hash": "sha256-abc", "instance_key": key, "node_count": 1},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.CreateInstance(context.Background(), CreateInstanceRequest{
		Template:    "sha256-abc",
		InstanceKey: &key,
		Params:      map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID() != "uuid-x" {
		t.Errorf("uuid: %q", got.UUID())
	}
}

func TestClient_GetInstance_NotFound(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/instances/missing", nil, http.StatusNotFound,
		map[string]any{"error": "instance not found"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	_, err := c.GetInstance(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Errorf("want IsNotFound, got %v", err)
	}
}

func TestClient_DeleteInstance_Conflict(t *testing.T) {
	srv := helperServer(t, http.MethodDelete, "/instances/x", nil, http.StatusConflict,
		map[string]any{"error": "instance is not in terminal state; wait for terminated_at to be set"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	err := c.DeleteInstance(context.Background(), "x")
	if !IsConflict(err) {
		t.Errorf("want IsConflict, got %v", err)
	}
}

func TestClient_ListInstanceNodes(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/instances/x/nodes", nil, http.StatusOK,
		map[string]any{
			"nodes": []map[string]any{
				{"id": "n1", "instance_id": "x", "node_type": "hello", "state": "fresh", "dependencies": []string{}, "retry_counter": 0, "action_index": 0, "created_at": "2026-05-02T00:00:00Z", "updated_at": "2026-05-02T00:00:00Z"},
			},
		},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.ListInstanceNodes(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "n1" {
		t.Errorf("got %+v", got.Nodes)
	}
}

func TestClient_GetNode(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/nodes/n1", nil, http.StatusOK,
		map[string]any{"id": "n1", "instance_id": "x", "node_type": "h", "state": "fresh", "dependencies": []string{}, "retry_counter": 0, "action_index": 0, "created_at": "t", "updated_at": "t"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.GetNode(context.Background(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "n1" {
		t.Errorf("id: %q", got.ID)
	}
}

func TestClient_InvalidateNode(t *testing.T) {
	srv := helperServer(t, http.MethodPost, "/nodes/n1/invalidate",
		map[string]any{"reason": "manual"},
		http.StatusOK,
		map[string]any{"ok": true},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.InvalidateNode(context.Background(), "n1", InvalidateNodeRequest{Reason: "manual"}); err != nil {
		t.Fatal(err)
	}
}

func TestClient_ResetNode(t *testing.T) {
	srv := helperServer(t, http.MethodPost, "/nodes/n1/reset", nil, http.StatusOK,
		map[string]any{"ok": true},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.ResetNode(context.Background(), "n1"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_ListEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("instance_id") != "x" {
			t.Errorf("instance_id missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events": []map[string]any{
				{"id": 1, "kind": "node_state_changed", "payload": map[string]any{}, "occurred_at": "t"},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.ListEvents(context.Background(), ListEventsQuery{InstanceID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].ID != 1 {
		t.Errorf("got %+v", got.Events)
	}
}

func TestClient_AdminForceFire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/scheduled-nodes/") {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.AdminForceFire(context.Background(), "n1"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_Health(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/health", nil, http.StatusOK,
		map[string]any{"status": "ok", "supervisors": []any{}, "node_counts": map[string]int{"fresh": 0}},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" {
		t.Errorf("status: %q", got.Status)
	}
}

func TestClient_5xxError(t *testing.T) {
	srv := helperServer(t, http.MethodGet, "/health", nil, http.StatusInternalServerError,
		map[string]any{"error": "boom"},
	)
	defer srv.Close()
	c := NewClient(srv.URL)
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if IsNotFound(err) || IsConflict(err) || IsBadRequest(err) {
		t.Errorf("misclassified %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error message missing body: %v", err)
	}
}
