// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package locks

import (
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
	return claimproducer.ErrScopesConflictUnsupportedFallback(a, b)
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
