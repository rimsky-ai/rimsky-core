// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: signal
package signal

import (
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
)

type TypePath string

type Signal struct {
	Type    TypePath
	Payload eventpayload.Payload
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

func PathPtr(t TypePath) *TypePath { return &t }
