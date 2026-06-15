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
	// @deliberate: nil receiver evaluates true unconditionally (always-match sentinel).
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
	// @constraint: exact-subscription compile enforces payload-field whitelist; terminal/success has no error_class.
	_, err := CompileWhen("terminal/success", "payload.error_class == 'x'")
	if err == nil {
		t.Fatalf("CompileWhen: expected unknown-field error")
	}
	if !strings.Contains(err.Error(), "error_class") {
		t.Fatalf("CompileWhen error should name the missing field; got %v", err)
	}
}

func TestCompileWhen_PrefixBindsDyn(t *testing.T) {
	// @deliberate: prefix subscriptions bind payload as dyn — compile skips field check, runtime missing-key returns false via safe-navigation.
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
