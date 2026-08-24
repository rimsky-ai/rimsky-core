// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestRetryRPCWithBackoff_SucceedsWithoutRetryOnFirstAttempt(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retryRPCWithBackoff(context.Background(), shared.SilentLogger{}, "TEST.RPCRETRY.ATTEMPTED",
		func(int, error) []any { return nil },
		func(context.Context) error {
			calls++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one attempt on immediate success, got %d", calls)
	}
}

func TestRetryRPCWithBackoff_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	calls := 0
	transient := errors.New("transient")
	err := retryRPCWithBackoff(context.Background(), shared.SilentLogger{}, "TEST.RPCRETRY.ATTEMPTED",
		func(int, error) []any { return nil },
		func(context.Context) error {
			calls++
			if calls < subscribeRetryAttempts {
				return transient
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if calls != subscribeRetryAttempts {
		t.Fatalf("expected %d attempts, got %d", subscribeRetryAttempts, calls)
	}
}

func TestRetryRPCWithBackoff_ExhaustsAttemptsAndReturnsLastError(t *testing.T) {
	t.Parallel()
	calls := 0
	persistent := errors.New("persistent")
	var loggedAttempts []int
	err := retryRPCWithBackoff(context.Background(), shared.SilentLogger{}, "TEST.RPCRETRY.ATTEMPTED",
		func(attempt int, _ error) []any {
			loggedAttempts = append(loggedAttempts, attempt)
			return nil
		},
		func(context.Context) error {
			calls++
			return persistent
		},
	)
	if !errors.Is(err, persistent) {
		t.Fatalf("expected the last attempt's error, got %v", err)
	}
	if calls != subscribeRetryAttempts {
		t.Fatalf("expected %d attempts, got %d", subscribeRetryAttempts, calls)
	}
	if len(loggedAttempts) != subscribeRetryAttempts {
		t.Fatalf("expected a log call per failed attempt, got %d", len(loggedAttempts))
	}
}

func TestRetryRPCWithBackoff_CtxCancelDuringBackoffStopsRetrying(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryRPCWithBackoff(ctx, shared.SilentLogger{}, "TEST.RPCRETRY.ATTEMPTED",
		func(int, error) []any { return nil },
		func(context.Context) error {
			calls++
			if calls == 1 {
				cancel()
			}
			return errors.New("fails every time")
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled once the backoff sleep observes the cancellation, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected the retry loop to stop after cancellation instead of running all %d attempts, got %d calls",
			subscribeRetryAttempts, calls)
	}
}
