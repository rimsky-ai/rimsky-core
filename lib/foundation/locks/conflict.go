// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"bytes"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func ModeCoexists(intentA claimproducer.Intent, semA claimproducer.WriteSemantics, intentB claimproducer.Intent, semB claimproducer.WriteSemantics) bool {
	syncA := isSync(semA)
	syncB := isSync(semB)
	if syncA != syncB {
		return true
	}
	rwA := intentA == claimproducer.IntentReadWrite
	rwB := intentB == claimproducer.IntentReadWrite
	if syncA {
		return !rwA && !rwB
	}
	return !(rwA && rwB)
}

// @concept: claim-scope
func ClaimScopesByteEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return bytes.Equal(a, b)
}

func isSync(ws claimproducer.WriteSemantics) bool {
	switch ws {
	case claimproducer.WriteSemanticsSync, claimproducer.WriteSemanticsBlockingAsync, claimproducer.WriteSemanticsReadOnly:
		return true
	case claimproducer.WriteSemanticsStagedAsync:
		return false
	}
	return true
}
