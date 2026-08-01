// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func seedActiveKey(t *testing.T, h authTestHarness) {
	t.Helper()
	_, hash, err := auth.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := h.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return h.tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID(uuid.New()),
			Name:        "seed-active-" + uuid.NewString(),
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   h.state.Clock.Now(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed active key: %v", err)
	}
}

func TestIsAnonymousMode_CachedWithinTTLThenRefreshesAfterExpiry(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	clock, ok := h.state.Clock.(*shared.ControllableClock)
	if !ok {
		t.Fatalf("harness clock is not a *shared.ControllableClock: %T", h.state.Clock)
	}

	anon, err := h.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("IsAnonymousMode (initial): %v", err)
	}
	if !anon {
		t.Fatalf("IsAnonymousMode (initial): got false want true (zero active keys)")
	}

	seedActiveKey(t, h)

	anon, err = h.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("IsAnonymousMode (within TTL): %v", err)
	}
	if !anon {
		t.Fatalf("IsAnonymousMode (within TTL): got false want true; " +
			"the 1s TTL cache must still report the stale anon=true reading instead of re-querying")
	}

	clock.Advance(anonCacheTTL)

	anon, err = h.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("IsAnonymousMode (after TTL): %v", err)
	}
	if anon {
		t.Fatalf("IsAnonymousMode (after TTL): got true want false; " +
			"once the cache entry's TTL has elapsed the next call must re-query and observe the newly active key")
	}
}

func TestInvalidateAnonCache_BypassesTTLLocally(t *testing.T) {
	h := newUnseededAuthTestHarness(t)

	anon, err := h.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("IsAnonymousMode (initial): %v", err)
	}
	if !anon {
		t.Fatalf("IsAnonymousMode (initial): got false want true (zero active keys)")
	}

	seedActiveKey(t, h)
	h.state.InvalidateAnonCache()

	anon, err = h.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("IsAnonymousMode (after invalidate): %v", err)
	}
	if anon {
		t.Fatalf("IsAnonymousMode (after invalidate): got true want false; " +
			"InvalidateAnonCache must force an immediate re-query rather than waiting out the TTL")
	}
}
