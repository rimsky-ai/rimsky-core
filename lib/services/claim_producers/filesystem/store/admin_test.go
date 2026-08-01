// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func TestAdminBumpToHead_204(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
	pp := &PickPolicy{
		Root: sub, OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp: action.Action{Kind: action.Recycle}, VisibilityTimeout: time.Minute,
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)
	must(t, st.runSync("@r", pp))

	handler := st.AdminHandler()
	req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
		strings.NewReader(`{"folder":"beta"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204; body=%s", w.Code, w.Body.String())
	}
	o, _ := st.Open(context.Background(), "c", "@r")
	var p struct{ Folder string }
	must(t, json.Unmarshal(o.Result.Payload, &p))
	if p.Folder != "beta" {
		t.Errorf("after bump, expected beta picked first; got %s", p.Folder)
	}
}

func TestAdminBumpToHead_404FolderMissing(t *testing.T) {
	st, _, _ := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	handler := st.AdminHandler()
	req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
		strings.NewReader(`{"folder":"nonexistent"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminBumpToHead_409InProgress(t *testing.T) {
	st, root, sub := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	_, _ = st.Open(context.Background(), "c", "@r")
	handler := st.AdminHandler()
	req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
		strings.NewReader(`{"folder":"alpha"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminBumpToHead_400UnknownSelector(t *testing.T) {
	st, _, _ := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	handler := st.AdminHandler()
	req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40nope",
		strings.NewReader(`{"folder":"x"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestAdminBumpToHead_400InvalidFolder(t *testing.T) {
	st, _, _ := newRingStore(t, action.Action{Kind: action.Recycle}, action.Action{Kind: action.Recycle})
	handler := st.AdminHandler()
	cases := []struct {
		name   string
		folder string
	}{
		{"forward_slash", "foo/bar"},
		{"forward_slash_traversal", "foo/../../etc"},
		{"backslash", `foo\bar`},
		{"dot_dot", ".."},
		{"single_dot", "."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"folder":` + jsonString(c.folder) + `}`
			req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
				strings.NewReader(body))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("folder=%q got %d, want 400; body=%s",
					c.folder, w.Code, w.Body.String())
			}
		})
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
