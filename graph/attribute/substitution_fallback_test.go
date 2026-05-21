// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package attributes

import (
	"encoding/json"
	"errors"
	"testing"
)

// Tests for the substitution fallback operator (2026-05-20 per-run keying
// spec). Wire shape: `{{<directive> | <literal>}}`.

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
	got, ok := val.(float64)
	if !ok || got != 42 {
		t.Fatalf("got %v (%T), want 42.0", val, val)
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
	// `deps.X.Y` returns ErrMissingSource (retired-form pointer). The
	// fallback DOES fire because the error IS a missing-source error —
	// this verifies the fallback path handles substantive missing
	// sources, not just absent-in-context references.
	val, err := SubstituteValue(`{{deps.X.Y | "default"}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default" {
		t.Fatalf("got %v, want default", val)
	}
}

// TestFallbackOperator_RetiredDepsFormFallsThrough pins the current
// behavior of `{{deps.X.Y | "default"}}`: the retired-form
// deprecation-pointer error from `resolveDirectiveValueRaw` is an
// `ErrMissingSource` (it carries the migration-pointer message), so
// the fallback operator silently swallows it and resolves to the
// literal default. This is consistent with spec semantics — the
// retired form is treated like any other missing source — but pins
// the current behavior so a future change to make retirement-pointer
// errors fatal-even-with-fallback would be a visible test break.
func TestFallbackOperator_RetiredDepsFormFallsThrough(t *testing.T) {
	val, err := SubstituteValue(`{{deps.X.Y | "default"}}`, ResolveContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default" {
		t.Fatalf("got %v, want default (retirement pointer eaten by fallback)", val)
	}
}

func TestFallbackOperator_ChainsRejected(t *testing.T) {
	_, err := SubstituteValue(`{{nodes.X.attribute.Y | nodes.Z.attribute.W | "default"}}`, ResolveContext{})
	if err == nil {
		t.Fatalf("expected error for multi-pipe chain")
	}
	// Per spec the chain rejection must be a FATAL grammar error, NOT
	// an ErrMissingSource — otherwise optional fields would silently
	// drop and required fields would never surface the malformed-
	// directive case as a validation failure.
	if IsMissingSource(err) {
		t.Fatalf("expected fatal grammar error, got ErrMissingSource: %v", err)
	}
	var chainErr *ErrFallbackChain
	if !errors.As(err, &chainErr) {
		t.Fatalf("expected ErrFallbackChain, got %T: %v", err, err)
	}
}

func TestFallbackOperator_NumberLiteral_RejectsNonJSONNumberShapes(t *testing.T) {
	// json.Unmarshal admits exactly JSON-number forms. Test that
	// strconv-shapes (`NaN`, `Inf`, `.5`) are rejected at runtime;
	// the validator separately rejects them at registration.
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

// TestFallbackOperator_ChainDetectionInsideRightOperand exercises the
// chain-detection guard at `resolveDirectiveValue`: when the right
// operand contains a further `|`, the resolver returns an
// `ErrFallbackChain` BEFORE attempting to resolve the left. The
// guard fires regardless of whether the left would have resolved or
// missed, so this case does not exercise the
// `if !IsMissingSource(err) { return nil, err }` branch — that
// branch is defensive against a future leaf returning a non-missing
// error from the left operand; no current leaf does, so it is not
// reachable through the public API today.
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

// TestFallbackOperator_MalformedLeftDepsFallsThrough pins the present-
// day behavior when the left-operand directive's underlying data is
// structurally broken (here: malformed JSON in Deps). The leaf
// resolver normalizes the decode failure to `ErrMissingSource`, which
// makes `IsMissingSource` true and the fallback fires.
//
// The `if !IsMissingSource(err) { return nil, err }` branch in
// `resolveDirectiveValue` is defensive against a future leaf that
// returns a different (non-missing) error type from the left operand.
// No current leaf surfaces such an error, so the branch is not
// directly testable through the public API today. If a future change
// adds a leaf that returns a non-missing fatal error from a
// SubstituteValue input shape like the one below, this test will
// flip (the fallback will stop firing and an error will surface),
// forcing a review of the fallback contract at that point.
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
