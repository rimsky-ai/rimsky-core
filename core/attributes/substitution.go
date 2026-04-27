// Substitution: single-pass `{{...}}` resolution per spec §10.
//
// @blessed-invariant 11 — Userdata is opaque to rimsky.
//
// No code path in this package inspects, parses, substitutes, or validates
// `userdata`. The grammar implemented here enumerates exactly three source
// kinds — `deps.<n>.<f...>`, `claim.<store>.<f...>`, `params.<key>` — and
// nothing else. A directive that does not match one of those shapes is a
// resolution error, not a fallback to userdata. (Spec §18 invariant 11.)

package attributes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/fallguy/rimsky/core/store"
)

// ResolveContext carries the three substitution source kinds, plus the
// declared `required` set for the attribute schema (used by callers to
// distinguish required-failure from optional-omission). Substitute itself
// returns ErrMissingSource on any unresolved reference; the caller is
// responsible for routing required-failures to template_resolution_failed
// and silently dropping optional-failures.
//
// Field-shape contract:
//   - Deps: keyed by upstream node name (template-relative, e.g. the value
//     of `nodes[].type`). Each entry is the upstream's
//     rimsky_node_attributes.data unmarshalled into a map[string]any.
//   - Claims: keyed by store name as it appears in the node's
//     `stores[*].name`. ClaimResult.Payload is the user-data payload from
//     the claimed item; resolution paths are `payload.<...>`.
//   - Params: rimsky_instances.params unmarshalled into a map[string]any.
//
// All three maps may be nil; nil is treated as empty.
type ResolveContext struct {
	Deps   map[string]map[string]any
	Claims map[string]store.ClaimResult
	Params map[string]any
}

// ErrMissingSource is returned by Substitute when a directive cannot
// resolve. The caller decides whether the failure is fatal: required
// attribute fields raise template_resolution_failed; optional fields are
// silently omitted. The Directive field is the raw text inside the
// `{{...}}` braces (without the braces) for log/event annotation.
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
// Single-pass: regex finds every `{{...}}`; we replace each with its
// resolved value or fail closed. A resolved value containing `{{...}}` is
// not re-scanned (recursion is not performed; spec §10.3).
var directivePattern = regexp.MustCompile(`\{\{([^}]*)\}\}`)

// Substitute performs a single-pass replacement of `{{...}}` directives in
// rawValue against ctx. Per spec §10.3:
//
//   - Single pass; no recursion. A substituted value containing `{{...}}`
//     is treated as literal text.
//   - A directive that does not match one of the three known source kinds
//     is an error.
//   - A directive whose target does not resolve returns ErrMissingSource.
//     The caller decides whether to treat that as fatal (required field /
//     region pattern / lock name) or as an omission (optional attribute
//     field).
//
// Empty-string results: a directive that resolves to the empty string
// (e.g. `params.foo` where the param is `""`) is returned verbatim as
// `""`. Spec §10.3 requires region-resolution to reject empty strings
// with `template_resolution_failed`; that check is the responsibility of
// the caller (the region-substitution pass), not Substitute itself.
// Substitute is grammar-and-resolve only; it does not enforce
// per-target-kind validity.
//
// Substitute does not consult any state outside ctx; it does not read
// userdata, the database, or environment. (See @blessed-invariant 11
// above.)
func Substitute(rawValue string, ctx ResolveContext) (string, error) {
	// Fast path: no directives at all.
	if !strings.Contains(rawValue, "{{") {
		return rawValue, nil
	}

	var firstErr error
	out := directivePattern.ReplaceAllStringFunc(rawValue, func(match string) string {
		if firstErr != nil {
			return match
		}
		// match includes the surrounding {{}}; strip to get the inside.
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

// resolveDirective parses one directive (without surrounding braces) and
// looks it up in ctx. Returns ErrMissingSource for unresolved references
// or a fmt.Errorf for unknown source kinds (the latter is a template /
// caller bug, not a runtime miss; both are returned as errors and the
// caller decides routing).
func resolveDirective(directive string, ctx ResolveContext) (string, error) {
	parts := strings.Split(directive, ".")
	if len(parts) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "directive must have at least <kind>.<field>"}
	}
	switch parts[0] {
	case "deps":
		return resolveDeps(directive, parts[1:], ctx.Deps)
	case "claim":
		return resolveClaim(directive, parts[1:], ctx.Claims)
	case "params":
		return resolveParams(directive, parts[1:], ctx.Params)
	default:
		return "", &ErrMissingSource{Directive: directive, Reason: "unknown source kind " + parts[0]}
	}
}

// resolveDeps handles `deps.<node-name>.<field-path>`. The first segment
// after `deps.` is the upstream node name; the remainder is a dot-notation
// field path into that upstream's attributes data.
func resolveDeps(directive string, rest []string, deps map[string]map[string]any) (string, error) {
	if len(rest) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "deps directive needs <node>.<field>"}
	}
	nodeName := rest[0]
	fieldPath := rest[1:]
	data, ok := deps[nodeName]
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "no upstream node " + nodeName}
	}
	val, ok := walkPath(any(data), fieldPath)
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "field path not found"}
	}
	return stringify(val), nil
}

// resolveClaim handles `claim.<store-name>.payload.<field-path>`. Per spec
// §5.7 the path is rooted at `payload.` so the directive grammar requires
// the literal segment `payload` immediately after the store name.
// Anything else (e.g. a future `claim.<store>.metadata.<...>`) would be a
// new top-level segment in ClaimResult that does not exist today; we
// reject it as missing rather than silently returning empty.
func resolveClaim(directive string, rest []string, claims map[string]store.ClaimResult) (string, error) {
	if len(rest) < 3 {
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive needs <store>.payload.<field>"}
	}
	storeName := rest[0]
	if rest[1] != "payload" {
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive second segment must be 'payload'"}
	}
	fieldPath := rest[2:]
	cr, ok := claims[storeName]
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "no claim for store " + storeName}
	}
	if cr.Payload == nil {
		return "", &ErrMissingSource{Directive: directive, Reason: "claim payload is nil"}
	}
	val, ok := walkPath(cr.Payload, fieldPath)
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "payload field path not found"}
	}
	return stringify(val), nil
}

// resolveParams handles `params.<key>`. Spec §10 specifies single-key
// access (no nesting): the params source is the flat
// `rimsky_instances.params` map. We do permit dot-notation walks for
// future-proofing (treating params as a map[string]any tree), but the
// canonical form is a single key.
func resolveParams(directive string, rest []string, params map[string]any) (string, error) {
	if len(rest) == 0 {
		return "", &ErrMissingSource{Directive: directive, Reason: "params directive needs <key>"}
	}
	val, ok := walkPath(any(params), rest)
	if !ok {
		return "", &ErrMissingSource{Directive: directive, Reason: "param key not found"}
	}
	return stringify(val), nil
}

// walkPath follows a dot-notation path into a tree of map[string]any (and
// any other JSON-decoded shape). Returns ok=false on any missing segment
// or non-map intermediate. The caller treats nil as missing — a `null`
// JSON value is functionally the same as "field absent" for substitution
// purposes (spec §10.3).
func walkPath(root any, path []string) (any, bool) {
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

// stringify renders a JSON-decoded value as a substitution-string. The
// rule is "use Go's %v default for primitives, and JSON-shape for
// composites" — but in practice substitution is overwhelmingly used for
// strings, numbers, and booleans (the JSON Schema source-driven fields are
// typed primitives). Maps and slices substituted as raw `%v` are not
// useful but also not wrong — a template asking to substitute a map into
// a region glob will produce a region glob that fails downstream
// validation, which is the correct outcome. Substitution does not enforce
// per-target type rules; the caller's downstream validation does.
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
