// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type fakeStream struct{ events []*genv1.ExecuteEvent }

func (f *fakeStream) send(ev *genv1.ExecuteEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStream) terminal() *genv1.StreamClose {
	for _, ev := range f.events {
		if sc := ev.GetStreamClose(); sc != nil {
			return sc
		}
	}
	return nil
}

func buildReq(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(ud)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier-http", Userdata: st}
}

func TestExecuteCore_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) == 0 {
			t.Errorf("expected body")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             srv.URL,
		"body":            map[string]any{"k": "v"},
		"expected_status": []any{float64(200)},
	})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil || term.GetSuccess() == nil {
		t.Fatalf("expected Success, got: %+v", term)
	}
}

func TestExecuteCore_StatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             srv.URL,
		"expected_status": []any{float64(200)},
	})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %+v", term)
	}
	if errOut.GetErrorClass() != "verifier_failed" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecuteCore_MissingURL(t *testing.T) {
	executor := NewServer(false)
	req := buildReq(t, map[string]any{})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	if term.GetError().GetErrorClass() != "invalid_userdata" {
		t.Errorf("error_class: %s", term.GetError().GetErrorClass())
	}
}
