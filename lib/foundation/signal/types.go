// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package signal owns the canonical hierarchical type-path taxonomy,
// payload schemas, CEL filter language, and audit-emit pathway for
// node-run-transition signals.
//
//	@concept: signal
//
// A Signal is the unified emission shape for any transition that
// affects a node-run. Every signal carries:
//
//   - a TypePath (slash-separated, hierarchical, validated against the
//     canonical taxonomy enumerated in taxonomy.go);
//   - a Payload (map[string]any whose shape is the per-type payload
//     struct from payloads.go, marshalled to a map at emit time).
//
// The signal travels two paths once produced:
//
//  1. Cascade walker — receivers' subscription edges keyed by
//     TypePath prefix; CEL when: predicates evaluated against the
//     payload gate wait-set insertion.
//  2. Audit log — every signal writes one rimsky_events row with
//     kind = string(TypePath) and payload = Signal.Payload.
//
// The two paths are independent: audit emission is unconditional;
// cascade-fire is subscriber-driven. See concept:signal for invariants.
package signal

import "strings"

// TypePath is the canonical, slash-delimited hierarchical path that
// classifies a Signal. Examples:
//
//	"terminal/success"
//	"terminal/error/http/timeout"
//	"transient/retry/3/agent/rate_limited"
//	"attribute/budget_cents/changed"
//	"event/discovered"
//
// Validated against the canonical taxonomy by ValidateTypePath. The
// first slash-delimited segment is the TopLevelKind.
type TypePath string

// Signal is the wire envelope for any node-run transition. Type
// classifies the transition; Payload carries the per-type structured
// data (its shape is the corresponding struct in payloads.go,
// converted to a map at construction time).
type Signal struct {
	Type    TypePath
	Payload map[string]any
}

// TopLevelKind names one of the four canonical top-level kinds in the
// signal taxonomy. The TypePath's first slash-delimited segment must
// be one of these values; anything else is rejected by
// ValidateTypePath.
type TopLevelKind string

const (
	// KindTerminal is emitted exactly once per run, at the moment the
	// run settles. Leaves: success, error/*, park/snooze,
	// park/await_callback, infra/*.
	KindTerminal TopLevelKind = "terminal"

	// KindTransient is emitted during the lifetime of a dispatch for
	// transitions observers may want to react to but that don't
	// finish the dispatch. Leaves: retry/<n>/<class>,
	// heartbeat_missed, await_async.
	KindTransient TopLevelKind = "transient"

	// KindAttribute is emitted when an upstream node writes an
	// attribute. Leaf: <key>/changed.
	KindAttribute TopLevelKind = "attribute"

	// KindEvent is emitted when an executor produces a non-terminal
	// named event. Leaf: <name>.
	KindEvent TopLevelKind = "event"
)

// TopLevel returns the first slash-delimited segment of the TypePath
// as a TopLevelKind. Returns the empty TopLevelKind if the leading
// segment is not one of the four canonical values — callers can treat
// empty as "not a recognized top-level kind."
func (t TypePath) TopLevel() TopLevelKind {
	s := string(t)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	k := TopLevelKind(s)
	switch k {
	case KindTerminal, KindTransient, KindAttribute, KindEvent:
		return k
	}
	return ""
}

// HasPrefix reports whether the TypePath matches the given prefix
// pattern. A prefix that ends with "*" matches if the TypePath starts
// with the prefix's leading segments (the "*" is stripped before
// comparison); a prefix without a trailing "*" matches only on exact
// equality.
//
// The trailing-"*" form is the project's only wildcard syntax.
// Positional wildcards (e.g., "terminal/*/foo") are not supported and
// are rejected at subscription registration by
// ValidateSubscriptionType.
func (t TypePath) HasPrefix(prefix TypePath) bool {
	p := string(prefix)
	if strings.HasSuffix(p, "*") {
		return strings.HasPrefix(string(t), strings.TrimSuffix(p, "*"))
	}
	return string(t) == p
}
