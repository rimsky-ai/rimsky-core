// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substitution: single-pass `{{...}}` resolution per spec §16.
//
// Five recognized source kinds:
//
//   - {{deps.<node>.<field>}} — upstream node's persisted attributes
//   - {{claim.<alias>.address}} — live claim's address bytes
//   - {{claim.<alias>.payload.<field>}} — live claim's payload at named path
//   - {{claim.<alias>.scope}} — live claim's scope bytes
//   - {{params.<key>}} — instance-level config params
//
// @blessed-invariant 11 — Userdata is opaque to rimsky.
//
//	No code path in this package inspects, parses, substitutes, or
//	validates `userdata`. The grammar implemented here enumerates exactly
//	the source kinds above and nothing else.
//
// @blessed-invariant 20 — Claim content is inert in Rimsky.
//
//	walkPath (below) is the single sanctioned introspection site for
//	substitution-leaf extraction from claim content. The function
//	lazy-unmarshals into a transient map[string]any only inside the
//	leaf-extraction call and discards it after extraction. The
//	stringifyRaw helper (below) is the sanctioned shape-flattening
//	site for top-level address/scope directives — it unwraps a
//	JSON-string value, otherwise returns the raw bytes verbatim, and
//	performs no logging, normalization, or transformation. All other
//	code paths must treat ClaimResult fields as opaque bytes (no
//	logging, no pretty-printing, no traces). One additional sanctioned
//	exception lives outside this package:
//	`core/supervisor/runner_dispatch.go::makeStoreHandle`, the wire-
//	encoding site that projects address/payload into a
//	google.protobuf.Struct for the executor protocol.
//
// Error paths in this package never include the value being walked —
// only the path tokens — to preserve invariant 20 in error/event
// surfaces.

package attributes

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/fallguy/rimsky/foundation/locks"
)

// ResolveContext carries the three substitution source kinds plus the
// declared `required` set for the attribute schema (used by callers to
// distinguish required-failure from optional-omission). Substitute
// itself returns ErrMissingSource on any unresolved reference; the
// caller is responsible for routing required-failures to
// template_resolution_failed and silently dropping optional-failures.
//
// Field-shape contract:
//
//   - Deps: keyed by upstream node name (template-relative). Each entry
//     is the upstream's rimsky_node_attributes.data as opaque
//     json.RawMessage; resolution paths walk into it lazily.
//   - Claim: keyed by per-claim alias (defaults to store name). Each
//     entry carries Address / Payload / Scope as opaque bytes.
//   - Params: rimsky_instances.params as opaque json.RawMessage.
//
// All three maps may be nil; nil is treated as empty.
type ResolveContext struct {
	Deps   map[string]json.RawMessage
	Claim  map[string]locks.ClaimResult
	Params json.RawMessage
}

// ErrMissingSource is returned by Substitute when a directive cannot
// resolve. The caller decides whether the failure is fatal: required
// attribute fields raise template_resolution_failed; optional fields
// are silently omitted. The Directive field is the raw text inside the
// `{{...}}` braces (without the braces) for log/event annotation.
//
// Per invariant 20, Reason MUST NOT include the value being walked —
// only path tokens.
type ErrMissingSource struct {
	Directive string
	Reason    string
}

func (e *ErrMissingSource) Error() string {
	return fmt.Sprintf("attributes: missing source for {{%s}}: %s", e.Directive, e.Reason)
}

// IsMissingSource reports whether err is (or wraps) an ErrMissingSource.
func IsMissingSource(err error) bool {
	var m *ErrMissingSource
	return errors.As(err, &m)
}

// directivePattern captures the inside of a single `{{...}}` directive.
var directivePattern = regexp.MustCompile(`\{\{([^}]*)\}\}`)

// Substitute performs a single-pass replacement of `{{...}}` directives
// in rawValue against ctx. Per spec §16.3:
//
//   - Single pass; no recursion. A substituted value containing
//     `{{...}}` is treated as literal text.
//   - A directive that does not match one of the known source kinds
//     is an error.
//   - A directive whose target does not resolve returns
//     ErrMissingSource. The caller decides whether to treat that as
//     fatal (required field) or as an omission (optional field).
//
// Substitute does not consult any state outside ctx; it does not read
// userdata, the database, or environment.
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

// resolveDirective parses one directive (without surrounding braces)
// and looks it up in ctx. Returns ErrMissingSource for unresolved
// references or unknown source kinds.
func resolveDirective(directive string, ctx ResolveContext) (string, error) {
	parts := strings.Split(directive, ".")
	if len(parts) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "directive must have at least <kind>.<field>"}
	}
	switch parts[0] {
	case "deps":
		return resolveDeps(directive, parts[1:], ctx.Deps)
	case "claim":
		return resolveClaim(directive, parts[1:], ctx.Claim)
	case "params":
		return resolveParams(directive, parts[1:], ctx.Params)
	default:
		return "", &ErrMissingSource{Directive: directive, Reason: "unknown source kind " + parts[0]}
	}
}

// resolveDeps handles `deps.<node-name>.<field-path>`. The first
// segment after `deps.` is the upstream node name; the remainder is a
// dot-notation field path into that upstream's attributes data
// (opaque bytes; lazy-decoded inside walkPath).
func resolveDeps(directive string, rest []string, deps map[string]json.RawMessage) (string, error) {
	if len(rest) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "deps directive needs <node>.<field>"}
	}
	nodeName := rest[0]
	fieldPath := rest[1:]
	data, ok := deps[nodeName]
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "no upstream node " + nodeName}
	}
	val, ok := walkPath(data, fieldPath)
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "field path not found"}
	}
	return stringify(val), nil
}

// resolveClaim handles three sub-shapes per spec §16.1:
//
//   - claim.<alias>.address  → leaf is ClaimResult.Address bytes
//   - claim.<alias>.payload.<field-path>
//   - claim.<alias>.scope    → leaf is ClaimResult.Scope bytes
//
// The alias is the per-claim name within the node (defaulting to the
// store name when not explicitly set).
func resolveClaim(directive string, rest []string, claims map[string]locks.ClaimResult) (string, error) {
	if len(rest) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive needs <alias>.{address|scope|payload.<field>}"}
	}
	alias := rest[0]
	cr, ok := claims[alias]
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "no claim for alias " + alias}
	}
	switch rest[1] {
	case "address":
		if len(rest) != 2 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.address takes no further field path"}
		}
		if len(cr.Address) == 0 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim address is empty"}
		}
		return stringifyRaw(cr.Address), nil
	case "scope":
		if len(rest) != 2 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.scope takes no further field path"}
		}
		if len(cr.Scope) == 0 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim scope is empty"}
		}
		return stringifyRaw(cr.Scope), nil
	case "payload":
		if len(rest) < 3 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.payload directive needs payload.<field>"}
		}
		if len(cr.Payload) == 0 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim payload is empty"}
		}
		val, ok := walkPath(cr.Payload, rest[2:])
		if !ok {
			return "", &ErrMissingSource{Directive: directive, Reason: "payload field path not found"}
		}
		return stringify(val), nil
	default:
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive second segment must be address|scope|payload"}
	}
}

// resolveParams handles `params.<key>` (and dot-notation walks for
// nested params).
func resolveParams(directive string, rest []string, params json.RawMessage) (string, error) {
	if len(rest) == 0 {
		return "", &ErrMissingSource{Directive: directive, Reason: "params directive needs <key>"}
	}
	if len(params) == 0 {
		return "", &ErrMissingSource{Directive: directive, Reason: "params is empty"}
	}
	val, ok := walkPath(params, rest)
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "param key not found"}
	}
	return stringify(val), nil
}

// walkPath is the sanctioned introspection site for payload field-walks
// (per blessed invariant 20). It accepts opaque bytes
// (json.RawMessage), lazy-unmarshals into a transient map[string]any
// only inside this function, walks the named field path, and returns
// the leaf value. The transient map is discarded when the function
// returns; no caller sees the intermediate decoded shape.
//
// @blessed-invariant 20: this is the sanctioned introspection site for
//
//	payload field-walks. The companion sanctioned sites (address/scope
//	shape-flattening and the wire-encoding projection) are documented
//	at the top of this file.
//
// Returns ok=false on any missing segment or non-map intermediate.
// `null` is treated as missing.
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

// stringify renders a JSON-decoded value as a substitution-string.
// The rule is "use Go's %v default for primitives, and JSON-shape for
// composites" — but in practice substitution is overwhelmingly used
// for strings, numbers, and booleans.
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

// stringifyRaw extracts a sensible string from raw JSON bytes for
// substitution at top-level claim address/scope directives. Strings
// unwrap (drop the surrounding quotes); other shapes pass through
// verbatim. Per invariant 20, this is the sanctioned shape-flattening
// site for address/scope leaves (walkPath is the sanctioned site for
// payload field-walks); the function does not log, hash, or
// transform — it returns bytes the caller embeds in a downstream
// substitution string. Keep these two sites in lock-step with the
// invariant 20 doc-block at the top of this file.
func stringifyRaw(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
