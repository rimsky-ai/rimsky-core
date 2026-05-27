// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// CEL integration for subscription when: predicates. The subscription
// surface offers `type: <path>` + optional `when: <cel-expression>`
// pairs; this file compiles when: into a CompiledPredicate the
// cascade walker evaluates against a Signal at walk time.
//
// Binding rules per concept:signal:
//
//   - For exact-type subscriptions (no trailing "*"), the CEL env
//     binds `payload` as a map<string, dyn> at evaluation time, but
//     field references are checked against the resolved payload
//     schema at compile time (via AST walk).  References to fields
//     not in the schema are rejected at registration with a precise
//     error naming the missing field.
//
//   - For prefix-type subscriptions (trailing "*"), the env binds
//     `payload` as a map<string, dyn> unconditionally; no field-name
//     check.  Predicates against unknown fields evaluate to a
//     missing-key error at runtime, which Eval surfaces as a false
//     match (the spec's "safe-navigation" semantic).
//
//   - `type` is always bound as a string (the actual signal's
//     TypePath as a string).
//
// See concept:signal for the broader contract.

package signal

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
)

// CompiledPredicate is an opaque wrapper over a compiled CEL program
// for a subscription when: expression. Construct via CompileWhen;
// evaluate against a Signal via Eval. A nil CompiledPredicate is the
// "no predicate, always match" sentinel and Eval handles it.
//
// The `subscriptionType` and `whenSrc` fields are captured at compile
// time and threaded into the Eval-error slog line so an operator
// triaging "which receiver's when: is broken?" can disambiguate when
// multiple subscriptions on the same signal-type fire.
type CompiledPredicate struct {
	program          cel.Program
	subscriptionType TypePath
	whenSrc          string
}

// CompileWhen compiles a CEL when: expression for the given
// subscription typeSpec. typeSpec is the subscription's `type:` field
// — either an exact emit-shape path (parse-checked against the
// resolved payload schema) or a trailing-"*" prefix (no field-name
// check, payload bound as dyn map).
//
// Empty when returns (nil, nil) — the canonical "always match"
// sentinel. Eval handles nil receivers.
//
// Returns wrapped errors on:
//   - CEL syntax error;
//   - field reference not in the resolved payload schema (exact-type
//     only).
func CompileWhen(typeSpec TypePath, when string) (*CompiledPredicate, error) {
	if when == "" {
		return nil, nil
	}
	env, err := buildEnv()
	if err != nil {
		return nil, fmt.Errorf("signal.CompileWhen: build CEL env: %w", err)
	}
	parsed, issues := env.Parse(when)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("invalid CEL expression %q: %w", when, issues.Err())
	}
	checked, issues := env.Check(parsed)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("invalid CEL expression %q: %w", when, issues.Err())
	}
	// Exact-type subscriptions: field-name check against the payload
	// schema. Prefix-type subscriptions skip the check.
	if !strings.HasSuffix(string(typeSpec), "*") {
		if schemaType, ok := PayloadSchemaForType(typeSpec); ok {
			if err := checkPayloadFields(checked, schemaType); err != nil {
				return nil, fmt.Errorf("invalid CEL expression %q: %w", when, err)
			}
		}
	}
	prog, err := env.Program(checked)
	if err != nil {
		return nil, fmt.Errorf("signal.CompileWhen: build program: %w", err)
	}
	return &CompiledPredicate{
		program:          prog,
		subscriptionType: typeSpec,
		whenSrc:          when,
	}, nil
}

// Eval evaluates the predicate against a Signal. A nil receiver
// returns (true, nil) — the always-match sentinel. The activation
// binds `type` to the signal's TypePath as a string and `payload` to
// the signal's payload as a map.
//
// CEL eval errors (e.g. a field reference that doesn't resolve on
// the actual payload, type mismatch on a comparator, overflow,
// panic-recovered runtime errors) are surfaced as the spec's
// safe-navigation default: (false, nil). The eval error is logged
// via the package-level slog default with the signal type-path for
// triage — visible without breaking the cascade walker on an
// expected missing-key.
func (p *CompiledPredicate) Eval(s Signal) (bool, error) {
	if p == nil {
		return true, nil
	}
	in := map[string]any{
		"type":    string(s.Type),
		"payload": s.Payload,
	}
	if in["payload"] == nil {
		in["payload"] = map[string]any{}
	}
	out, _, err := p.program.Eval(in)
	if err != nil {
		// Per the spec's prefix-binding rule: field references that
		// don't resolve on the actual payload evaluate to a "no such
		// key" error at runtime; surface as a false match rather
		// than bubbling up. Genuine evaluation errors (e.g., type
		// mismatch on a comparator) also short-circuit to false.
		// Log so operator-side mistakes (typo in a `when:` field
		// reference, mismatched types) surface in observability
		// without breaking the cascade walk on every emission.
		slog.Default().Warn("signal.CompiledPredicate.Eval: CEL eval error; treating as no-match",
			"signal_type", string(s.Type),
			"subscription_type", string(p.subscriptionType),
			"when", p.whenSrc,
			"error", err.Error())
		return false, nil
	}
	b, ok := out.(types.Bool)
	if !ok {
		// Predicate that doesn't return a bool is treated as a
		// non-match (defensive — Check should have rejected
		// non-boolean predicates).
		return false, nil
	}
	return bool(b), nil
}

// buildEnv constructs the shared CEL env used for all subscription
// predicates. type is bound as string; payload is bound as
// map<string, dyn> (the dyn map handles both exact-type and
// prefix-type subscriptions at evaluation time; field-name
// constraint for exact-type happens via AST walk at compile time).
func buildEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("type", cel.StringType),
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
	)
}

// checkPayloadFields walks the compiled AST and rejects any
// `payload.<field>` reference whose field is not in the resolved
// payload schema. Only direct `payload.X` selects are checked;
// chained selects like `payload.foo.bar` only check `foo`.
//
// Field names are matched against the struct's JSON tag (when
// present) or the lowercased Go field name (mirroring the encoding/
// json default). This matches what the runtime sees when the payload
// struct is marshalled to a map for emission.
func checkPayloadFields(checked *cel.Ast, schemaType reflect.Type) error {
	if schemaType == nil {
		return nil
	}
	allowed := schemaFieldSet(schemaType)
	if len(allowed) == 0 {
		return nil
	}
	nav := ast.NavigateAST(checked.NativeRep())
	selects := ast.MatchDescendants(nav, ast.KindMatcher(ast.SelectKind))
	var missing []string
	seen := map[string]struct{}{}
	for _, sel := range selects {
		s := sel.AsSelect()
		operand := s.Operand()
		if operand.Kind() != ast.IdentKind {
			continue
		}
		if operand.AsIdent() != "payload" {
			continue
		}
		field := s.FieldName()
		if _, ok := allowed[field]; ok {
			continue
		}
		if _, dup := seen[field]; dup {
			continue
		}
		seen[field] = struct{}{}
		missing = append(missing, field)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("field reference(s) not in payload schema %s: %s",
		schemaType.Name(), strings.Join(missing, ", "))
}

// schemaFieldSet returns the set of valid CEL field names for the
// given struct type. Names come from the json struct tag (first
// comma-separated segment) when present, else the lowercased Go
// field name.
func schemaFieldSet(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{})
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok && tag != "" {
			if comma := strings.IndexByte(tag, ','); comma >= 0 {
				tag = tag[:comma]
			}
			if tag != "" && tag != "-" {
				name = tag
			}
		}
		out[name] = struct{}{}
	}
	return out
}
