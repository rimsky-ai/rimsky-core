// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"bytes"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func ModeCoexists(intentA, intentB claimproducer.Intent, sem claimproducer.WriteSemantics) bool {
	rwA := intentA == claimproducer.IntentReadWrite
	rwB := intentB == claimproducer.IntentReadWrite
	if mvccPassThrough(sem) {
		return !(rwA && rwB)
	}
	return !rwA && !rwB
}

// @concept: claim-scope
func ClaimScopesByteEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return bytes.Equal(a, b)
}

func mvccPassThrough(ws claimproducer.WriteSemantics) bool {
	switch ws {
	case claimproducer.WriteSemanticsStagedAsync:
		return true
	case claimproducer.WriteSemanticsSync, claimproducer.WriteSemanticsBlockingAsync, claimproducer.WriteSemanticsReadOnly:
		return false
	}
	panic(fmt.Sprintf("ModeCoexists: unknown write-semantics %q", ws))
}
