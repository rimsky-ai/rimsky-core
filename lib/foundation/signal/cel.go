// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package signal

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type CompiledPredicate struct {
	program          cel.Program
	subscriptionType TypePath
	whenSrc          string
}

func CompileWhen(typeSpec TypePath, when string) (*CompiledPredicate, error) {
	return CompileWhenWithBodyFields(typeSpec, when, nil)
}

func (p *CompiledPredicate) Source() string {
	if p == nil {
		return ""
	}
	return p.whenSrc
}

// @concept: message-schema
func CompileWhenWithBodyFields(typeSpec TypePath, when string, bodyFields map[string]struct{}) (*CompiledPredicate, error) {
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
	if schemaType, ok := PayloadSchemaForType(typeSpec); ok {
		if err := checkPayloadFields(checked, schemaType); err != nil {
			return nil, fmt.Errorf("invalid CEL expression %q: %w", when, err)
		}
	}
	// @story: typed-message-substitution
	if bodyFields != nil {
		if err := checkAttributesDeltaFields(checked, bodyFields); err != nil {
			return nil, fmt.Errorf("invalid CEL expression %q: %w", when, err)
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

// @concept: message-schema
func checkAttributesDeltaFields(checked *cel.Ast, allowed map[string]struct{}) error {
	nav := ast.NavigateAST(checked.NativeRep())
	selects := ast.MatchDescendants(nav, ast.KindMatcher(ast.SelectKind))
	var missing []string
	seen := map[string]struct{}{}
	for _, sel := range selects {
		s := sel.AsSelect()
		operand := s.Operand()
		if operand.Kind() != ast.SelectKind {
			continue
		}
		inner := operand.AsSelect()
		innerOp := inner.Operand()
		if innerOp.Kind() != ast.IdentKind {
			continue
		}
		if innerOp.AsIdent() != "payload" {
			continue
		}
		if inner.FieldName() != "attributes_delta" {
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
	return fmt.Errorf("payload.attributes_delta.<field> reference(s) not in message body_schema: %s",
		strings.Join(missing, ", "))
}

func (p *CompiledPredicate) Eval(s Signal) (bool, error) {
	if p == nil {
		return true, nil
	}
	in := map[string]any{
		"type":    string(s.Type),
		"payload": s.Payload.Map(),
	}
	out, _, err := p.program.Eval(in)
	if err != nil {
		slog.Default().Warn("SIGNAL.CELPREDICATE.EVALFAILED", "site", "signal.CompiledPredicate.Eval", "detail", "treating the predicate as no-match",
			"signal_type", string(s.Type),
			"subscription_type", string(p.subscriptionType),
			"when", p.whenSrc,
			"error", err.Error())
		return false, nil
	}
	b, ok := out.(types.Bool)
	if !ok {
		return false, nil
	}
	return bool(b), nil
}

func buildEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("type", cel.StringType),
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
	)
}

func checkPayloadFields(checked *cel.Ast, schemaType protoreflect.MessageDescriptor) error {
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

func schemaFieldSet(d protoreflect.MessageDescriptor) map[string]struct{} {
	out := make(map[string]struct{})
	if d == nil {
		return out
	}
	fields := d.Fields()
	for i := 0; i < fields.Len(); i++ {
		out[string(fields.Get(i).Name())] = struct{}{}
	}
	return out
}
