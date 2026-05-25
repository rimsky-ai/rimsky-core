// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguyconsulting/rimsky/executors/verifier-shape-checks/errorclasses"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
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

func buildReq(t *testing.T, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier", Attributes: st}
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

func TestExecuteCore_FailureClassifiesAsHierarchicalCheckFailed(t *testing.T) {
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
	// Hierarchical leaf carries the failed check's kind suffix per
	// `concept:signal`. Validator accepts this via the declared
	// `verifier/check_failed/*` wildcard.
	if errOut.GetErrorClass() != "verifier/check_failed/pk_unique" {
		t.Errorf("error_class: got %q, want verifier/check_failed/pk_unique", errOut.GetErrorClass())
	}
}

func TestExecuteCore_InvalidAttributesRejected(t *testing.T) {
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
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

// TestCapabilities_AdvertisesHierarchicalErrorClasses confirms the
// observability surface advertises the canonical verifier/* leaves
// imported from `pkg:executors/verifier-shape-checks/errorclasses`.
func TestCapabilities_AdvertisesHierarchicalErrorClasses(t *testing.T) {
	obs := NewObservabilityServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	declared := caps.GetDeclaredErrorClasses()
	want := errorclasses.Declared()
	if len(declared) != len(want) {
		t.Fatalf("declared_error_classes: got %v, want %v", declared, want)
	}
	for i, c := range declared {
		if c != want[i] {
			t.Errorf("declared[%d]: got %q, want %q", i, c, want[i])
		}
		if c == "" {
			t.Errorf("declared[%d]: empty string", i)
		}
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
