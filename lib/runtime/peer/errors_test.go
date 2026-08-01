// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package peer

import (
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestGateSplitScope(t *testing.T) {
	t.Parallel()
	if err := GateSplitScope(claimproducer.Capabilities{SupportsSplitScope: false}); !errors.Is(err, claimproducer.ErrSplitScopeUnsupported) {
		t.Fatalf("GateSplitScope(unsupported) = %v, want ErrSplitScopeUnsupported", err)
	}
	if err := GateSplitScope(claimproducer.Capabilities{SupportsSplitScope: true}); err != nil {
		t.Fatalf("GateSplitScope(supported) = %v, want nil", err)
	}
}

func TestGateScopesConflict(t *testing.T) {
	t.Parallel()
	fallback, gated := GateScopesConflict(claimproducer.Capabilities{SupportsScopesConflict: false}, []byte("same"), []byte("same"))
	if !gated {
		t.Fatalf("GateScopesConflict(unsupported) gated = false, want true")
	}
	if !fallback {
		t.Fatalf("GateScopesConflict(unsupported, equal scopes) fallback = false, want true")
	}

	fallback, gated = GateScopesConflict(claimproducer.Capabilities{SupportsScopesConflict: false}, []byte("a"), []byte("b"))
	if !gated || fallback {
		t.Fatalf("GateScopesConflict(unsupported, differing scopes) = (%v, %v), want (false, true)", fallback, gated)
	}

	_, gated = GateScopesConflict(claimproducer.Capabilities{SupportsScopesConflict: true}, []byte("a"), []byte("b"))
	if gated {
		t.Fatalf("GateScopesConflict(supported) gated = true, want false")
	}
}
