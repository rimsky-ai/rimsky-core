// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Substitution: single-pass `{{...}}` resolution per spec §16.
//
// Five recognized source kinds:
//
//   - {{nodes.<node>.attribute.<field>}} — upstream node's persisted attributes
//   - {{nodes.<node>.event.<event_name>.<field>}} — upstream node's most recent named-event payload
//   - {{claim.<alias>.address}} — live claim's address bytes
//   - {{claim.<alias>.payload.<field>}} — live claim's payload at named path
//   - {{claim.<alias>.scope}} — live claim's scope bytes
//   - {{params.<key>}} — instance-level config params
//
// The post-2026-05-14 `nodes.X.attribute.Y` form replaces the legacy
// `deps.X.Y` form: per the subscription-cascade resolution, substitution
// refs auto-subscribe the receiver to (sender=X, topic=attribute, name=Y).
//
// @blessed-invariant 11 — Userdata is inert in Rimsky.
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
//	`runtime/runner_dispatch.go::makeClaimHandle`, the wire-
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
//     json.RawMessage; resolution paths walk into it lazily. The field
//     name is historical — under the post-2026-05-14 grammar, callers
//     populate it by walking the receiver's subscription-edge inverse
//     map (every sender referenced by a `{{nodes.X.attribute.Y}}`
//     directive or an explicit `subscribes:` entry).
//   - Claim: keyed by per-claim alias (defaults to store name). Each
//     entry carries Address / Payload / Scope as opaque bytes.
//   - Params: rimsky_instances.params as opaque json.RawMessage.
//
// All three maps may be nil; nil is treated as empty.
//
// EventLookup is an optional callable resolving the most recent named
// event payload for substitutions of the form
// `nodes.<emitter_node>.event.<event_name>.<json_path>` (per the
// 2026-05-08 platform-extensions plan F4). When nil, every such
// directive returns ErrMissingSource. The lookup returns
// (payload, ok=true) when a row exists in the per-instance event ledger
// for (emitter, event_name); the most recent emission wins. Payload
// bytes are walked via the same walkPath machinery attribute and claim
// payloads use, preserving @blessed-invariant 20 (and 11 by-extension —
// userdata is not consulted here either).
type ResolveContext struct {
	Deps   map[string]json.RawMessage
	Claim  map[string]locks.ClaimResult
	Params json.RawMessage

	// EventLookup, when non-nil, resolves named-event payload bytes for
	// the (emitter, eventName) pair. ok=false means "no emission yet" and
	// translates to ErrMissingSource.
	EventLookup func(emitter, eventName string) (payload json.RawMessage, ok bool)

	// TriggerMessagePayload is the opaque payload bytes of the trigger
	// message bound to this frame (the rimsky_messages row whose
	// frame_id matches the dispatch). Empty / nil → no trigger; any
	// `{{trigger.message.payload.X}}` directive returns ErrMissingSource.
	// Bound by the runtime at dispatch time from the frame's trigger
	// message lookup. Inert in rimsky per @blessed-invariant 11/20/21.
	//
	// Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Substitution-layer extensions.
	TriggerMessagePayload json.RawMessage

	// ChildPartitionKey is the per-child-run partition key value bound
	// for fan-out leaf dispatches. Empty string → no binding; any
	// `{{child.partition_key}}` directive returns ErrMissingSource.
	// Bound by the runtime fan-out dispatcher (E7) at child dispatch.
	// Per spec §Substitution-layer extensions.
	ChildPartitionKey string
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

// SubstituteValue is the value-returning sibling of Substitute. The
// resolution mode is chosen by the input string's shape:
//
//   - Whole-directive mode: if `trim(input)` is exactly one `{{...}}`
//     directive (the directive pattern matches the entire trimmed input
//     with no surrounding characters), the resolved JSON value is
//     returned as-is — object (`map[string]any`), array (`[]any`),
//     string, number (`float64`), or bool.
//   - Embedded mode: if the input contains literal text alongside
//     directives or contains multiple directives, each directive's
//     resolution is stringified and concatenated. The result is a Go
//     string. Current `Substitute` behaviour.
//
// The discriminator is the input string's shape, not the directive's
// kind or the resolved value's type.
//
// JSON `null` along the resolution path is treated as ErrMissingSource
// (existing `walkPath` behaviour); whole-directive lift of `null` is
// not supported.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 3.
//
// @concept: attribute
func SubstituteValue(rawValue string, ctx ResolveContext) (any, error) {
	trimmed := strings.TrimSpace(rawValue)
	if trimmed != "" && directivePattern.FindString(trimmed) == trimmed {
		// Whole-directive mode: the trimmed input is exactly one directive.
		inside := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
		if inside == "" {
			return nil, &ErrMissingSource{Directive: inside, Reason: "empty directive"}
		}
		return resolveDirectiveValue(inside, ctx)
	}
	// Embedded mode (or no directive at all): fall through to the
	// string-returning path.
	s, err := Substitute(rawValue, ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// resolveDirective parses one directive (without surrounding braces)
// and looks it up in ctx. Returns ErrMissingSource for unresolved
// references or unknown source kinds.
//
// Recognized source kinds (post-2026-05-14):
//
//	nodes.<X>.attribute.<key>...   — upstream attribute walk
//	nodes.<X>.event.<name>.<path>  — upstream named-event walk
//	claim.<alias>.{address|scope|payload.<key>...}
//	params.<key>...
//
// The legacy `deps.<X>.<key>` form is retired; callers receive a
// migration-pointer error.
//
// String-returning wrapper around resolveDirectiveValue. Composite
// values (objects/arrays) are JSON-encoded for embedded-mode
// concatenation; primitives go through stringify.
func resolveDirective(directive string, ctx ResolveContext) (string, error) {
	// Claim address/scope take the legacy string-flattening path
	// (stringifyRaw) rather than walkPath, so we route them through the
	// string-returning resolveClaim directly.
	parts := strings.Split(directive, ".")
	if len(parts) >= 2 && parts[0] == "claim" {
		return resolveClaim(directive, parts[1:], ctx.Claim)
	}
	val, err := resolveDirectiveValue(directive, ctx)
	if err != nil {
		return "", err
	}
	return stringifyAny(val), nil
}

// resolveDirectiveValue is the value-returning sibling of
// resolveDirective. Returns the resolved JSON value (string, float64,
// bool, []any, or map[string]any) for the directive. ErrMissingSource
// for unresolved references, unknown kinds, or JSON `null` along the
// path (consistent with walkPath's existing null-as-missing behaviour).
func resolveDirectiveValue(directive string, ctx ResolveContext) (any, error) {
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
	default:
		return nil, &ErrMissingSource{Directive: directive, Reason: "unknown source kind " + parts[0]}
	}
}

// stringifyAny renders a JSON-decoded value for the embedded-mode
// substitution path: primitives via stringify, composites via JSON
// encoding. Composite-in-embedded-mode is rare (the dominant use case
// is whole-directive lift via SubstituteValue), but keeping the
// embedded-mode behaviour predictable matches the spec's promise of
// "current behaviour preserved" for the embedded path.
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

// resolveTriggerValue handles `{{trigger.message.payload(.field-path?)}}`
// directives — the spec §Substitution-layer extensions form that binds
// the trigger message of the current frame into the substitution
// context. The trigger message is the rimsky_messages row whose
// frame_id matches the dispatched run's frame; the runtime resolves it
// at dispatch and threads the payload bytes via
// ResolveContext.TriggerMessagePayload.
//
// Walks payload bytes via walkPath, the sanctioned introspection site
// for @blessed-invariant 11/20/21.
//
// An empty trailing field path (`trigger.message.payload`) resolves to
// the whole payload as a JSON-decoded value (per spec §Item 3 — bare-
// form pull).
//
// @blessed-invariant: messages are inert in rimsky. Message payload
// bytes are read by rimsky only here (via `walkPath` substitution
// against the trigger message) and at the persistence-layer fetch in
// `GET /messages/{id}` (control/controlapi/messages.go). Rimsky never
// logs, formats with `%v`, validates beyond schema gates, transforms,
// or includes payload bytes in error messages. Same opacity discipline
// as `@blessed-invariant 11/20/21` (userdata, claim content, blob
// content / named-event payloads).
func resolveTriggerValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	// Expected form: trigger.message.payload(.field-path…)?
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

// resolveChildValue handles `{{child.partition_key}}` directives — the
// spec §Substitution-layer extensions form that binds the per-child-run
// partition key for fan-out leaf dispatches.
//
// Per @blessed-invariant 11/20/21 the partition_key value is forwarded
// verbatim — no parsing, normalization, or logging.
func resolveChildValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) != 1 || rest[0] != "partition_key" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "child directive must be child.partition_key"}
	}
	if ctx.ChildPartitionKey == "" {
		return nil, &ErrMissingSource{Directive: directive, Reason: "no partition_key bound (fan-out leaf dispatch context only)"}
	}
	return ctx.ChildPartitionKey, nil
}

// resolveNodesValue handles two `nodes.<X>.<kind>(.<...>?)` directive
// forms:
//
//   - `nodes.<node>.attribute(.<field>?…)` — walks the upstream node's
//     persisted attributes data. Empty trailing field path resolves to
//     the whole attribute object (per spec §Item 3).
//   - `nodes.<emitter>.event.<event_name>(.<field>?…)` — walks the most
//     recent named-event payload via ResolveContext.EventLookup. Empty
//     trailing field path resolves to the whole event payload.
//
// Walks bytes via walkPath — the sanctioned introspection site for
// payload field-walks (@blessed-invariant 20).
func resolveNodesValue(directive string, rest []string, ctx ResolveContext) (any, error) {
	if len(rest) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive needs <node>.{attribute|event}[.<field>]"}
	}
	nodeName := rest[0]
	kind := rest[1]
	switch kind {
	case "attribute":
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
	case "event":
		// Expected: <node>.event.<event_name>(.<field-path…>)?
		if len(rest) < 3 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive needs <node>.event.<name>[.<field>]"}
		}
		eventName := rest[2]
		fieldPath := rest[3:]
		if ctx.EventLookup == nil {
			return nil, &ErrMissingSource{Directive: directive, Reason: "no event lookup configured"}
		}
		payload, ok := ctx.EventLookup(nodeName, eventName)
		if !ok || len(payload) == 0 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "no emission for event"}
		}
		val, ok := walkPath(payload, fieldPath)
		if !ok {
			return nil, &ErrMissingSource{Directive: directive, Reason: "event payload field path not found"}
		}
		return val, nil
	default:
		return nil, &ErrMissingSource{Directive: directive, Reason: "nodes directive second segment must be 'attribute' or 'event'"}
	}
}

// resolveClaim handles three sub-shapes per spec §16.1:
//
//   - claim.<alias>.address  → leaf is ClaimResult.Address bytes
//   - claim.<alias>.payload(.<field-path>?)
//   - claim.<alias>.scope    → leaf is ClaimResult.Scope bytes
//
// The alias is the per-claim name within the node (defaulting to the
// store name when not explicitly set). String-returning path retained
// so `address` and `scope` continue to flow through stringifyRaw (the
// sanctioned shape-flattening site for those top-level leaves).
//
// Empty trailing path on the `payload` branch resolves to the whole
// payload object (per spec §Item 3).
func resolveClaim(directive string, rest []string, claims map[string]locks.ClaimResult) (string, error) {
	if len(rest) < 2 {
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive needs <alias>.{address|scope|payload[.<field>]}"}
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
		if len(cr.Payload) == 0 {
			return "", &ErrMissingSource{Directive: directive, Reason: "claim payload is empty"}
		}
		val, ok := walkPath(cr.Payload, rest[2:])
		if !ok {
			return "", &ErrMissingSource{Directive: directive, Reason: "payload field path not found"}
		}
		return stringifyAny(val), nil
	default:
		return "", &ErrMissingSource{Directive: directive, Reason: "claim directive second segment must be address|scope|payload"}
	}
}

// resolveClaimValue is the value-returning sibling of resolveClaim.
// `address` and `scope` continue to surface as strings (the sanctioned
// shape-flattening output); `payload(.<field>?)` returns the
// JSON-decoded value at the named path or — with an empty trailing
// path — the whole payload object.
func resolveClaimValue(directive string, rest []string, claims map[string]locks.ClaimResult) (any, error) {
	if len(rest) < 2 {
		return nil, &ErrMissingSource{Directive: directive, Reason: "claim directive needs <alias>.{address|scope|payload[.<field>]}"}
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
	case "scope":
		if len(rest) != 2 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim.<alias>.scope takes no further field path"}
		}
		if len(cr.Scope) == 0 {
			return nil, &ErrMissingSource{Directive: directive, Reason: "claim scope is empty"}
		}
		return stringifyRaw(cr.Scope), nil
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
		return nil, &ErrMissingSource{Directive: directive, Reason: "claim directive second segment must be address|scope|payload"}
	}
}

// resolveParamsValue handles `params.<key>(.…)?` — walks the instance
// params blob and returns the leaf as a JSON-decoded value.
//
// The universal `len(parts) >= 2` guard at resolveDirective rejects a
// bare `params` directive (no field path); the spec deliberately keeps
// "whole params pull" out of the grammar. Consumers wanting it wrap
// their params in a top-level key (`params.config: {...}`) and pull
// `{{params.config}}`.
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
