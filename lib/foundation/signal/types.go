// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: signal
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
