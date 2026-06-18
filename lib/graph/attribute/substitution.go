// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type ResolveContext struct {
	Deps   map[string]json.RawMessage
	Claim  map[string]claimproducer.ClaimResult
	Params json.RawMessage

	TriggerMessagePayload json.RawMessage

	// @concept: message-schema
	TriggerMessageType string

	ChildPartitionKey string

	// @concept: message-schema
	RegistryDeclaredTypes map[string]struct{}
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
	case "deps":
		return nil, &ErrMissingSource{Directive: directive, Reason: "`deps.<X>.<Y>` retired; use `nodes.<X>.attribute.<Y>` (see spec 2026-05-14)"}
	case "claim":
		return resolveClaimValue(directive, parts[1:], ctx.Claim)
	case "params":
		return resolveParamsValue(directive, parts[1:], ctx.Params)
	case "nodes":
		return resolveNodesValue(directive, parts[1:], ctx)
	case "trigger":
		return resolveTriggerValue(directive, parts[1:], ctx)
	case "child":
		return resolveChildValue(directive, parts[1:], ctx)
	case "messages":
		return resolveMessagesValue(directive, parts[1:], ctx)
	default:
		return nil, &ErrMissingSource{Directive: directive, Reason: "unknown source kind " + parts[0]}
	}
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

func resolveTriggerValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "trigger directive needs trigger.message.payload[.<field>]"}
	}
	if rest[0] != "message" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "trigger directive second segment must be 'message'"}
	}
	if rest[1] != "payload" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "trigger directive third segment must be 'payload'"}
	}
	if len(ctx.TriggerMessagePayload) == 0 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no trigger message bound to this frame"}
	}
	val, ok := walkPath(ctx.TriggerMessagePayload, rest[2:])
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "trigger payload field path not found"}
	}
	return val, nil
}

// @concept: message-schema
func resolveMessagesValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) < 1 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "messages directive needs messages.<type>[.<field>]"}
	}
	declaredType := rest[0]
	if declaredType == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "messages directive has an empty <type>"}
	}
	if ctx.RegistryDeclaredTypes != nil {
		if _, ok := ctx.RegistryDeclaredTypes[declaredType]; !ok {
			return nil, &ErrMissingSource{
				Directive: directive,
				Reason:    "messages directive names a type not in the template's messages registry",
			}
		}
	}
	if ctx.TriggerMessageType == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no trigger message bound to this frame"}
	}
	if ctx.TriggerMessageType != declaredType {
		return nil, &ErrMissingSource{
			Directive: directive,
			Reason:    "frame's triggering message type does not match directive <type>",
		}
	}
	if len(ctx.TriggerMessagePayload) == 0 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "trigger message body is empty"}
	}
	fieldPath := rest[1:]
	val, ok := walkPath(ctx.TriggerMessagePayload, fieldPath)
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "message body field path not found"}
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

func resolveNodesValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive needs <node>.attribute[.<field>]"}
	}
	nodeName := rest[0]
	kind := rest[1]
	if kind != "attribute" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive second segment must be 'attribute'"}
	}
	fieldPath := rest[2:]
	data, ok := ctx.Deps[nodeName]
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no upstream node " + nodeName}
	}
	val, ok := walkPath(data, fieldPath)
	if !ok {
		return nil, &ErrMissingSource{Directive: directive, Reason: "attribute field path not found"}
	}
	return val, nil
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
