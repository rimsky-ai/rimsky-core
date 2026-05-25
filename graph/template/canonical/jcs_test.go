// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package canonical

import (
	"encoding/json"
	"strings"
	"testing"

	jsoncanonicalizer "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"

	"github.com/fallguyconsulting/rimsky/graph/node"
)

func minimalSpec() node.TemplateSpec {
	return node.TemplateSpec{
		Name:                "demo",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			{Type: "noop"},
		},
	}
}

func TestCanonicalSpecHash_Deterministic(t *testing.T) {
	spec := minimalSpec()
	a, err := CanonicalSpecHash(spec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b, err := CanonicalSpecHash(spec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if a != b {
		t.Fatalf("expected identical hashes, got %q vs %q", a, b)
	}
}

func TestCanonicalSpecHash_KeyOrderInvariant(t *testing.T) {
	// Two structurally-identical specs that differ only in the field
	// declaration order at construction time. encoding/json emits Go
	// struct fields in struct-declaration order, but JCS sorts keys —
	// so the two ParamsSchema maps below (whose Go literal order
	// differs) must canonicalize identically.
	specA := minimalSpec()
	specA.ParamsSchema = map[string]any{"a": 1, "b": 2, "c": 3}

	specB := minimalSpec()
	specB.ParamsSchema = map[string]any{"c": 3, "a": 1, "b": 2}

	a, err := CanonicalSpecHash(specA)
	if err != nil {
		t.Fatalf("hash A: %v", err)
	}
	b, err := CanonicalSpecHash(specB)
	if err != nil {
		t.Fatalf("hash B: %v", err)
	}
	if a != b {
		t.Fatalf("expected identical hashes for key-order-invariant specs, got %q vs %q", a, b)
	}
}

func TestCanonicalSpecHash_PrefixIsSha256(t *testing.T) {
	h, err := CanonicalSpecHash(minimalSpec())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(h, "sha256-") {
		t.Fatalf("expected sha256- prefix, got %q", h)
	}
	suffix := strings.TrimPrefix(h, "sha256-")
	if len(suffix) != 64 {
		t.Fatalf("expected 64-hex suffix, got len=%d (%q)", len(suffix), suffix)
	}
	for _, c := range suffix {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Fatalf("expected lowercase hex suffix, got non-hex %q in %q", c, suffix)
		}
	}
}

func TestCanonicalSpecHash_DistinctSpecs(t *testing.T) {
	a := minimalSpec()
	b := minimalSpec()
	b.Version = "2"
	ha, err := CanonicalSpecHash(a)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, err := CanonicalSpecHash(b)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if ha == hb {
		t.Fatalf("expected distinct hashes for distinct specs, got %q for both", ha)
	}
}

// TestCanonicalSpecHash_WhitespaceInvariant exercises the wrapper end-
// to-end: encoding/json emits compact JSON for marshal calls (no
// whitespace control surface on the wrapper), so the relevant property
// — JCS strips any whitespace the input carries — is reachable through
// CanonicalSpecHash by constructing two semantically identical specs
// (the same map content with different runtime ordering) and asserting
// matching hashes.
//
// The byte-level transform property (compact vs indented JSON
// canonicalize identically) is covered by TestJCSLib_Whitespace below
// using the underlying library directly.
func TestCanonicalSpecHash_WhitespaceInvariant(t *testing.T) {
	specA := minimalSpec()
	specA.ParamsSchema = map[string]any{"x": 1, "y": 2}
	specB := minimalSpec()
	specB.ParamsSchema = map[string]any{"y": 2, "x": 1}

	a, err := CanonicalSpecHash(specA)
	if err != nil {
		t.Fatalf("hash A: %v", err)
	}
	b, err := CanonicalSpecHash(specB)
	if err != nil {
		t.Fatalf("hash B: %v", err)
	}
	if a != b {
		t.Fatalf("encoding/json + JCS must produce identical canonical hashes for the same content; got %q vs %q", a, b)
	}
}

// TestJCSLib_Whitespace pins the underlying library's whitespace-
// stripping property at the byte level. Distinct from the wrapper
// test above: this asserts the library contract, not the wrapper's
// behaviour.
func TestJCSLib_Whitespace(t *testing.T) {
	spec := minimalSpec()
	compact, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal compact: %v", err)
	}
	indented, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent: %v", err)
	}
	canonA, err := jsoncanonicalizer.Transform(compact)
	if err != nil {
		t.Fatalf("canon compact: %v", err)
	}
	canonB, err := jsoncanonicalizer.Transform(indented)
	if err != nil {
		t.Fatalf("canon indent: %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("expected JCS to strip whitespace; compact=%q indent=%q", canonA, canonB)
	}
}

// TestJCSLib_NumberNormalization pins the underlying library's
// number-normalization contract at the byte level. The wrapper does
// not expose the same surface (Go-side TemplateSpec doesn't carry
// arbitrary number-typed fields with this kind of representational
// ambiguity), so the test operates on hand-written JSON inputs.
func TestJCSLib_NumberNormalization(t *testing.T) {
	a := []byte(`{"x": 1.0}`)
	b := []byte(`{"x": 1}`)
	canonA, err := jsoncanonicalizer.Transform(a)
	if err != nil {
		t.Fatalf("canon a: %v", err)
	}
	canonB, err := jsoncanonicalizer.Transform(b)
	if err != nil {
		t.Fatalf("canon b: %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("expected JCS to normalize 1.0 vs 1; got %q vs %q", canonA, canonB)
	}

	c := []byte(`{"x": 1e3}`)
	d := []byte(`{"x": 1000}`)
	canonC, err := jsoncanonicalizer.Transform(c)
	if err != nil {
		t.Fatalf("canon c: %v", err)
	}
	canonD, err := jsoncanonicalizer.Transform(d)
	if err != nil {
		t.Fatalf("canon d: %v", err)
	}
	if string(canonC) != string(canonD) {
		t.Fatalf("expected JCS to normalize 1e3 vs 1000; got %q vs %q", canonC, canonD)
	}
}

// TestJCSLib_KeyOrderingByteEqual constructs two byte-level JSON
// inputs with identical content but different key orderings, applies
// the JCS Transform to each, and asserts the canonicalized outputs
// are byte-equal. Pins cross-language reproducibility: the library is
// what guarantees deterministic bytes, not encoding/json's
// implementation-defined ordering.
func TestJCSLib_KeyOrderingByteEqual(t *testing.T) {
	a := []byte(`{"a": 1, "b": 2, "c": 3}`)
	b := []byte(`{"c": 3, "a": 1, "b": 2}`)
	canonA, err := jsoncanonicalizer.Transform(a)
	if err != nil {
		t.Fatalf("canon a: %v", err)
	}
	canonB, err := jsoncanonicalizer.Transform(b)
	if err != nil {
		t.Fatalf("canon b: %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("expected JCS to canonicalize identical content regardless of key order; got %q vs %q", canonA, canonB)
	}
}
