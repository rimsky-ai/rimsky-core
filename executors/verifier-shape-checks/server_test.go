// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// fakeStream collects every event the server emits; assertion runs
// after executeCore returns.
type fakeStream struct {
	events []*genv1.ExecuteEvent
}

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

func buildReq(t *testing.T, userdata map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(userdata)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier", Userdata: st}
}

func TestExecuteCore_PassAllChecks(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
			map[string]any{"kind": "pk_unique", "config": map[string]any{"field": "id"}},
		},
		"rows": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		},
	})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	if term.GetSuccess() == nil {
		t.Errorf("expected Success, got %T", term.Outcome)
	}
}

func TestExecuteCore_FailureClassifiesAsVerifierFailed(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "pk_unique", "config": map[string]any{"field": "id"}},
		},
		"rows": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "a"},
		},
	})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", term.Outcome)
	}
	if errOut.GetErrorClass() != "verifier_failed" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecuteCore_InvalidUserdataRejected(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"rows": []any{map[string]any{"id": "x"}},
		// missing `checks`
	})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", term.Outcome)
	}
	if errOut.GetErrorClass() != "invalid_userdata" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecuteCore_StubProbeShortCircuits(t *testing.T) {
	srv := NewServer(true)
	req := buildReq(t, map[string]any{"stub_probe": true})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil || term.GetSuccess() == nil {
		t.Errorf("expected Success terminal in stub mode: %+v", term)
	}
}
