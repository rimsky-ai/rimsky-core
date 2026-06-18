// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type TypePath string

type Signal struct {
	Type    TypePath
	Payload map[string]any
}

type TopLevelKind string

const (
	KindTerminal TopLevelKind = "terminal"

	KindTransient TopLevelKind = "transient"

	KindAttribute TopLevelKind = "attribute"
)

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
	case KindTerminal, KindTransient, KindAttribute:
		return k
	}
	return ""
}

func (t TypePath) HasPrefix(prefix TypePath) bool {
	p := string(prefix)
	if strings.HasSuffix(p, "*") {
		return strings.HasPrefix(string(t), strings.TrimSuffix(p, "*"))
	}
	return string(t) == p
}
