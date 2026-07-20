// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"strings"
	"testing"
)

func TestCompileWhen_NilOnEmpty(t *testing.T) {
	got, err := CompileWhen("terminal/success", "")
	if err != nil {
		t.Fatalf("CompileWhen empty: %v", err)
	}
	if got != nil {
		t.Fatalf("CompileWhen empty: expected nil predicate, got %+v", got)
	}
	ok, err := got.Eval(Signal{Type: "terminal/success"})
	if err != nil {
		t.Fatalf("Eval nil: %v", err)
	}
	if !ok {
		t.Fatalf("Eval nil: expected true, got false")
	}
}

func TestCompileWhen_AcceptsValidExact(t *testing.T) {
	p, err := CompileWhen("terminal/error/http/timeout",
		"payload.error_class == 'http/timeout'")
	if err != nil {
		t.Fatalf("CompileWhen: %v", err)
	}
	if p == nil {
		t.Fatalf("CompileWhen returned nil predicate")
	}
	ok, err := p.Eval(Signal{
		Type:    "terminal/error/http/timeout",
		Payload: map[string]any{"error_class": "http/timeout"},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !ok {
		t.Fatalf("Eval: expected true")
	}
	ok, _ = p.Eval(Signal{
		Type:    "terminal/error/http/timeout",
		Payload: map[string]any{"error_class": "other"},
	})
	if ok {
		t.Fatalf("Eval mismatch: expected false")
	}
}

func TestCompileWhen_AcceptsValidPrefix(t *testing.T) {
	p, err := CompileWhen("terminal/*",
		"type.startsWith('terminal/error')")
	if err != nil {
		t.Fatalf("CompileWhen prefix: %v", err)
	}
	if p == nil {
		t.Fatalf("CompileWhen prefix returned nil")
	}
	ok, err := p.Eval(Signal{Type: "terminal/error/foo"})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !ok {
		t.Fatalf("Eval: expected true on terminal/error/foo")
	}
	ok, _ = p.Eval(Signal{Type: "terminal/success"})
	if ok {
		t.Fatalf("Eval: expected false on terminal/success")
	}
}

func TestCompileWhen_RejectsInvalidSyntax(t *testing.T) {
	_, err := CompileWhen("terminal/success", "payload.foo &&&")
	if err == nil {
		t.Fatalf("CompileWhen: expected syntax error")
	}
}

func TestCompileWhen_RejectsUnknownFieldExact(t *testing.T) {
	_, err := CompileWhen("terminal/success", "payload.error_class == 'x'")
	if err == nil {
		t.Fatalf("CompileWhen: expected unknown-field error")
	}
	if !strings.Contains(err.Error(), "error_class") {
		t.Fatalf("CompileWhen error should name the missing field; got %v", err)
	}
}

func TestCompileWhen_AttributesDeltaOnError(t *testing.T) {
	p, err := CompileWhen("terminal/error/agent/rate_limited",
		"'transient' in payload.attributes_delta")
	if err != nil {
		t.Fatalf("CompileWhen error AttributesDelta: %v", err)
	}
	if p == nil {
		t.Fatalf("CompileWhen returned nil predicate")
	}
	ok, err := p.Eval(Signal{
		Type: "terminal/error/agent/rate_limited",
		Payload: map[string]any{
			"error_class":      "agent/rate_limited",
			"attributes_delta": map[string]any{"transient": true},
		},
	})
	if err != nil {
		t.Fatalf("Eval error AttributesDelta: %v", err)
	}
	if !ok {
		t.Fatalf("Eval error AttributesDelta: expected true")
	}
}

func TestCompileWhenWithBodyFields_RejectsAttributesDeltaFieldNotInBodySchema(t *testing.T) {
	_, err := CompileWhenWithBodyFields("terminal/error/agent/rate_limited",
		"'transient' in payload.attributes_delta && payload.attributes_delta.unlisted_field == 'x'",
		map[string]struct{}{"transient": {}})
	if err == nil {
		t.Fatalf("CompileWhenWithBodyFields: expected rejection of a payload.attributes_delta field absent from the declared body schema")
	}
	if !strings.Contains(err.Error(), "unlisted_field") {
		t.Fatalf("CompileWhenWithBodyFields error should name the offending field; got %v", err)
	}
}

func TestCompileWhenWithBodyFields_AcceptsDeclaredAttributesDeltaField(t *testing.T) {
	p, err := CompileWhenWithBodyFields("terminal/error/agent/rate_limited",
		"payload.attributes_delta.transient == true",
		map[string]struct{}{"transient": {}})
	if err != nil {
		t.Fatalf("CompileWhenWithBodyFields: %v", err)
	}
	ok, err := p.Eval(Signal{
		Type: "terminal/error/agent/rate_limited",
		Payload: map[string]any{
			"error_class":      "agent/rate_limited",
			"attributes_delta": map[string]any{"transient": true},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !ok {
		t.Fatalf("Eval: expected true for a declared attributes_delta field")
	}
}

func TestCompileWhen_PrefixBindsDyn(t *testing.T) {
	p, err := CompileWhen("terminal/*", "payload.error_class == 'x'")
	if err != nil {
		t.Fatalf("CompileWhen prefix dyn: %v", err)
	}
	if p == nil {
		t.Fatalf("CompileWhen returned nil")
	}
	ok, _ := p.Eval(Signal{
		Type:    "terminal/success",
		Payload: map[string]any{"changed": true},
	})
	if ok {
		t.Fatalf("Eval missing key: expected false")
	}
	ok, _ = p.Eval(Signal{
		Type:    "terminal/error/x",
		Payload: map[string]any{"error_class": "x"},
	})
	if !ok {
		t.Fatalf("Eval matching: expected true")
	}
}
