// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @plumbline:allow-docstrings
//
// Six recognized source kinds:
//
//   - {{claim.<alias>.<address|claim_scope|payload[.<field>]>}}
//   - {{params.<key>[.<sub-field>...]}}
//   - {{nodes.<node-type>.attribute[.<field>...]}}
//   - {{messages.<message-type>[.<field>...]}}
//   - {{child.partition_key}}
//   - {{env.<VAR_NAME>}}
//
// `messages.<type>.<field>` is sugar for `nodes.<type>.attribute.<field>`
// — both resolve through the same `Deps` lookup. The only difference is
// the registration-time validation: a `messages.<type>` ref requires
// `<type>` to be declared in the template's `messages:` registry, where
// `nodes.<type>` requires `<type>` to be declared as a node-type.
//
// `env.<VAR_NAME>` reads from the supervisor process's environment at
// dispatch time. Names must match `[A-Za-z_][A-Za-z0-9_]*`. Unlike the
// graph-coupled kinds (nodes / messages) it induces no subscription
// edge; like params/claim/child it resolves against non-graph context.
package attributes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type ResolveContext struct {
	Deps   map[string]json.RawMessage
	Claim  map[string]claimproducer.ClaimResult
	Params json.RawMessage

	ChildPartitionKey string

	// @concept: message-schema
	RegistryDeclaredTypes map[string]struct{}

	// EnvLookup overrides the host-environment reader for {{env.<VAR>}}
	// resolution. Nil falls back to os.LookupEnv.
	EnvLookup func(name string) (string, bool)
}

type ErrMissingSource struct {
	Directive string
	Reason    string
}

func (e *ErrMissingSource) Error() string {
	return fmt.Sprintf("attributes: missing source for {{%s}}: %s", e.Directive, e.Reason)
}

func IsMissingSource(err error) bool {
	var m *ErrMissingSource
	return errors.As(err, &m)
}

type ErrFallbackChain struct {
	Directive string
}

func (e *ErrFallbackChain) Error() string {
	return fmt.Sprintf("attributes: fallback chains are not admitted in {{%s}}", e.Directive)
}

var directivePattern = regexp.MustCompile(`\{\{([^}]*)\}\}`)

func Substitute(rawValue string, ctx ResolveContext) (string, error) {
	if !strings.Contains(rawValue, "{{") {
		return rawValue, nil
	}
	var firstErr error
	out := directivePattern.ReplaceAllStringFunc(rawValue, func(match string) string {
		if firstErr != nil {
			return match
		}
		inside := strings.TrimSpace(match[2 : len(match)-2])
		if inside == "" {
			firstErr = &ErrMissingSource{Directive: inside, Reason: "empty directive"}
			return match
		}
		resolved, err := resolveDirective(inside, ctx)
		if err != nil {
			firstErr = err
			return match
		}
		return resolved
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// @concept: attribute
func SubstituteValue(rawValue string, ctx ResolveContext) (any, error) {
	trimmed := strings.TrimSpace(rawValue)
	if trimmed != "" && directivePattern.FindString(trimmed) == trimmed {
		inside := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
		if inside == "" {
			return nil, &ErrMissingSource{Directive: inside, Reason: "empty directive"}
		}
		return resolveDirectiveValue(inside, ctx)
	}
	s, err := Substitute(rawValue, ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func resolveDirective(directive string, ctx ResolveContext) (string, error) {
	val, err := resolveDirectiveValue(directive, ctx)
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", nil
	}
	return stringifyAny(val), nil
}

func resolveDirectiveValue(directive string, ctx ResolveContext) (any, error) {
	if idx := strings.Index(directive, "|"); idx >= 0 {
		leftRaw := strings.TrimSpace(directive[:idx])
		rightRaw := strings.TrimSpace(directive[idx+1:])
		if strings.Contains(rightRaw, "|") {
			return nil, &ErrFallbackChain{Directive: directive}
		}
		leftRaw = strings.TrimSpace(strings.TrimSuffix(leftRaw, "?"))
		val, err := resolveDirectiveValueRaw(leftRaw, ctx)
		if err == nil {
			return val, nil
		}
		if !IsMissingSource(err) {
			return nil, err
		}
		return parseFallbackLiteral(rightRaw)
	}
	lenient, stripped := parseLenientMarker(directive)
	val, err := resolveDirectiveValueRaw(stripped, ctx)
	if err == nil {
		return val, nil
	}
	if lenient && IsMissingSource(err) {
		return nil, nil
	}
	return nil, err
}

func parseLenientMarker(body string) (lenient bool, stripped string) {
	trimmed := strings.TrimSpace(body)
	if strings.HasSuffix(trimmed, "?") {
		return true, strings.TrimSpace(strings.TrimSuffix(trimmed, "?"))
	}
	return false, body
}

func resolveDirectiveValueRaw(directive string, ctx ResolveContext) (any, error) {
	parts := strings.Split(directive, ".")
	if len(parts) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "directive must have at least <kind>.<field>"}
	}
	switch parts[0] {
	case "claim":
		return resolveClaimValue(directive, parts[1:], ctx.Claim)
	case "params":
		return resolveParamsValue(directive, parts[1:], ctx.Params)
	case "nodes", "messages":
		return resolveSubstitutionValue(directive, parts[0], parts[1:], ctx)
	case "child":
		return resolveChildValue(directive, parts[1:], ctx)
	case "env":
		return resolveEnvValue(directive, parts[1:], ctx)
	default:
		return nil, &ErrMissingSource{Directive: directive, Reason: "unknown source kind " + parts[0]}
	}
}

var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// @decision: env-as-substitution-source-kind
func resolveEnvValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) != 1 || rest[0] == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "env directive must be env.<VAR_NAME>"}
	}
	name := rest[0]
	if !envVarNameRe.MatchString(name) {
		return nil, &ErrMissingSource{Directive: directive, Reason: "env var name must match [A-Za-z_][A-Za-z0-9_]*"}
	}
	lookup := ctx.EnvLookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	val, ok := lookup(name)
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "env var " + name + " is not set"}
	}
	return val, nil
}

func parseFallbackLiteral(raw string) (any, error) {
	if raw == "null" {
		return nil, nil
	}
	if raw == "true" {
		return true, nil
	}
	if raw == "false" {
		return false, nil
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err == nil {
			return s, nil
		}
		return nil, fmt.Errorf("invalid literal in fallback: %q", raw)
	}
	var n float64
	if err := json.Unmarshal([]byte(raw), &n); err == nil {
		return n, nil
	}
	return nil, fmt.Errorf("invalid literal in fallback: %q", raw)
}

func stringifyAny(v any) string {
	switch v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
	return stringify(v)
}

// @concept: message-schema
// @concept: node-subscription
func resolveSubstitutionValue(directive, prefix string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) < 1 {
		return nil, &ErrMissingSource{Directive: directive, Reason: prefix + " directive needs <type>[.<field>]"}
	}
	typeName := rest[0]
	if typeName == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: prefix + " directive has an empty <type>"}
	}
	fieldStart := 1
	if prefix == "nodes" {
		if len(rest) < 2 || rest[1] != "attribute" {
			return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive second segment must be 'attribute'"}
		}
		fieldStart = 2
	}
	if prefix == "messages" && ctx.RegistryDeclaredTypes != nil {
		if _, ok := ctx.RegistryDeclaredTypes[typeName]; !ok {
			return nil, &ErrMissingSource{
				Directive: directive,
				Reason:    "messages directive names a type not in the template's messages registry",
			}
		}
	}
	data, ok := ctx.Deps[typeName]
	if !ok {
		if prefix == "messages" {
			return nil, &ErrMissingSource{Directive: directive, Reason: "no delivered message of type " + typeName + " bound to this frame"}
		}
		return nil, &ErrMissingSource{Directive: directive, Reason: "no upstream node " + typeName}
	}
	val, ok := walkPath(data, rest[fieldStart:])
	if !ok {
		if prefix == "messages" {
			return nil, &ErrMissingSource{Directive: directive, Reason: "message body field path not found"}
		}
		return nil, &ErrMissingSource{Directive: directive, Reason: "attribute field path not found"}
	}
	return val, nil
}

func resolveChildValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) != 1 || rest[0] != "partition_key" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "child directive must be child.partition_key"}
	}
	if ctx.ChildPartitionKey == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no partition_key bound (fan-out leaf dispatch context only)"}
	}
	return ctx.ChildPartitionKey, nil
}

func resolveClaimValue(directive string, rest []string, claims map[string]claimproducer.ClaimResult) (any, error) {
	if len(rest) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "claim directive needs <alias>.{address|claim_scope|payload[.<field>]}"}
	}
	alias := rest[0]
	cr, ok := claims[alias]
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no claim for alias " + alias}
	}
	switch rest[1] {
	case "address":
		if len(rest) != 2 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.address takes no further field path"}
		}
		if len(cr.Address) == 0 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim address is empty"}
		}
		return stringifyRaw(cr.Address), nil
	case "claim_scope":
		if len(rest) != 2 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.claim_scope takes no further field path"}
		}
		if len(cr.ClaimScope) == 0 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim claim_scope is empty"}
		}
		return stringifyRaw(cr.ClaimScope), nil
	case "payload":
		if len(cr.Payload) == 0 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim payload is empty"}
		}
		val, ok := walkPath(cr.Payload, rest[2:])
		if !ok {
			return nil, &ErrMissingSource{Directive: directive, Reason: "payload field path not found"}
		}
		return val, nil
	default:
		return nil, &ErrMissingSource{Directive: directive, Reason: "claim directive second segment must be address|claim_scope|payload"}
	}
}

func resolveParamsValue(directive string, rest []string, params json.RawMessage) (any, error) {
	if len(rest) == 0 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "params directive needs <key>"}
	}
	if len(params) == 0 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "params is empty"}
	}
	val, ok := walkPath(params, rest)
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "param key not found"}
	}
	return val, nil
}

func walkPath(raw json.RawMessage, path []string) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false
	}
	cur := root
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[seg]
		if !ok {
			return nil, false
		}
		if v == nil {
			return nil, false
		}
		cur = v
	}
	if cur == nil {
		return nil, false
	}
	return cur, true
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func stringifyRaw(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
