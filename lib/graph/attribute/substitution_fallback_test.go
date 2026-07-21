// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attribute

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestFallbackOperator_DirectiveResolves(t *testing.T) {
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"X": json.RawMessage(`{"Y": "the-real-value"}`),
		},
	}
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "default"}}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "the-real-value" {
		t.Fatalf("got %v, want the-real-value", val)
	}
}

func TestFallbackOperator_DirectiveMissing_FallsThrough(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "default"}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default" {
		t.Fatalf("got %v, want default", val)
	}
}

func TestFallbackOperator_NullLiteral(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | null}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != nil {
		t.Fatalf("got %v, want nil", val)
	}
}

func TestFallbackOperator_NumberLiteral(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | 42}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(json.Number)
	if !ok || got != "42" {
		t.Fatalf("got %v (%T), want json.Number(42)", val, val)
	}
}

func TestFallbackOperator_NumberLiteral_LargeIntegerPreservesPrecision(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | 9007199254740993}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(json.Number)
	if !ok || got.String() != "9007199254740993" {
		t.Fatalf("got %v (%T), want json.Number(9007199254740993) exactly (no float64 rounding)", val, val)
	}
}

func TestFallbackOperator_BoolLiteral(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | true}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(bool)
	if !ok || !got {
		t.Fatalf("got %v (%T), want true", val, val)
	}
}

func TestFallbackOperator_MissingDirectiveFallsThroughEvenInvalidShape(t *testing.T) {
	val, err := SubstituteValue(`{{deps.X.Y | "default"}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default" {
		t.Fatalf("got %v, want default", val)
	}
}

func TestFallbackOperator_ChainsRejected(t *testing.T) {
	_, err := SubstituteValue(`{{nodes.X.attribute.Y | nodes.Z.attribute.W | "default"}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected error for multi-pipe chain")
	}
	if IsMissingSource(err) {
		t.Fatalf("expected fatal grammar error, got ErrMissingSource: %v", err)
	}
	var chainErr *ErrFallbackChain
	if !errors.As(err, &chainErr) {
		t.Fatalf("expected ErrFallbackChain, got %T: %v", err, err)
	}
}

func TestFallbackOperator_NumberLiteral_RejectsNonJSONNumberShapes(t *testing.T) {
	cases := []string{
		`{{nodes.X.attribute.Y | NaN}}`,
		`{{nodes.X.attribute.Y | Inf}}`,
		`{{nodes.X.attribute.Y | .5}}`,
	}
	for _, c := range cases {
		_, err := SubstituteValue(c, ResolveContext{})
		if err == nil {
			t.Errorf("expected error for non-JSON-number literal %q", c)
		}
	}
}

func TestFallbackOperator_ObjectLiteralRejected(t *testing.T) {
	_, err := SubstituteValue(`{{nodes.X.attribute.Y | {}}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected error for composite literal")
	}
}

func TestFallbackOperator_ChainDetectionInsideRightOperand(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "default" | "other"}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected fatal chain error; got value %v", val)
	}
	if IsMissingSource(err) {
		t.Fatalf("expected non-missing fatal error; got ErrMissingSource: %v", err)
	}
	var chainErr *ErrFallbackChain
	if !errors.As(err, &chainErr) {
		t.Fatalf("expected ErrFallbackChain, got %T: %v", err, err)
	}
	if val == "default" {
		t.Fatalf("non-missing error must NOT fire fallback; got literal %q", val)
	}
}

func TestFallbackOperator_MalformedLeftDepsFallsThrough(t *testing.T) {
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"X": json.RawMessage(`{not json at all`),
		},
	}
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "default"}}`, ctx)
	if err != nil {
		t.Fatalf("malformed deps + fallback: expected fallback to fire (leaf normalizes decode failure to ErrMissingSource); got error %v", err)
	}
	if val != "default" {
		t.Fatalf("expected fallback to resolve to \"default\"; got %v", val)
	}
}

func TestFallbackOperator_QuotedLiteralContainingPipe(t *testing.T) {
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "a|b"}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("quoted fallback literal containing a literal pipe: expected fallback to fire, got error %v", err)
	}
	if val != "a|b" {
		t.Fatalf("got %v, want \"a|b\"", val)
	}
}

func TestFallbackOperator_QuotedLiteralContainingPipe_DirectiveResolves(t *testing.T) {
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"X": json.RawMessage(`{"Y": "the-real-value"}`),
		},
	}
	val, err := SubstituteValue(`{{nodes.X.attribute.Y | "a|b"}}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "the-real-value" {
		t.Fatalf("got %v, want the-real-value (fallback must not fire when the directive resolves)", val)
	}
}

func TestFallbackOperator_QuotedLiteralWithPipe_StillDetectsRealChain(t *testing.T) {
	_, err := SubstituteValue(`{{nodes.X.attribute.Y | "a|b" | "other"}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected error for multi-pipe chain")
	}
	if IsMissingSource(err) {
		t.Fatalf("expected fatal grammar error, got ErrMissingSource: %v", err)
	}
	var chainErr *ErrFallbackChain
	if !errors.As(err, &chainErr) {
		t.Fatalf("expected ErrFallbackChain, got %T: %v", err, err)
	}
}

func TestLenientMarker_WithFallback_Rejected(t *testing.T) {
	_, err := SubstituteValue(`{{nodes.X.attribute.Y? | "default"}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected error for combined `?` marker and `| <literal>` fallback")
	}
	if IsMissingSource(err) {
		t.Fatalf("expected fatal grammar error, got ErrMissingSource: %v", err)
	}
	var conflictErr *ErrLenientFallbackConflict
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ErrLenientFallbackConflict, got %T: %v", err, err)
	}
}

func TestLenientMarker_WithFallback_RejectedEvenWhenDirectiveResolves(t *testing.T) {
	ctx := ResolveContext{
		Deps: map[string]json.RawMessage{
			"X": json.RawMessage(`{"Y": "the-real-value"}`),
		},
	}
	_, err := SubstituteValue(`{{nodes.X.attribute.Y? | "default"}}`, ctx)
	if err == nil {
		t.Fatalf("expected error for combined `?` marker and `| <literal>` fallback even when the directive would resolve")
	}
	var conflictErr *ErrLenientFallbackConflict
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ErrLenientFallbackConflict, got %T: %v", err, err)
	}
}
