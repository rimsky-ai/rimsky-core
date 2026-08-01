// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgpool

import (
	"context"
	"errors"
	"testing"
)

func TestBootDecouplesFromFirstCallerContext(t *testing.T) {
	p := New(Config{
		Image:    "postgres:14-alpine",
		Database: "pgpoolbootctx",
		User:     "pgpoolbootctx",
		Password: "pgpoolbootctx",
	})

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := p.boot(canceledCtx); err != nil {
		t.Fatalf("boot with an already-canceled first-caller context must still succeed "+
			"(boot must not inherit caller cancellation, or it poisons every later Acquire in the process): %v", err)
	}

	if err := p.boot(context.Background()); err != nil {
		t.Fatalf("boot on a later call must reuse the successful sync.Once result, got: %v", err)
	}
}

func TestRunPostgresWithRetryBackoffRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runPostgresWithRetry(ctx, Config{
		Image:    "postgres:14-alpine",
		Database: "pgpoolbackoffcancel",
		User:     "pgpoolbackoffcancel",
		Password: "pgpoolbackoffcancel",
	})
	if err == nil {
		t.Fatalf("runPostgresWithRetry with an already-canceled context must fail, not silently succeed")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runPostgresWithRetry with an already-canceled context must surface context.Canceled "+
			"(the inter-attempt backoff must select on ctx.Done(), not block on a bare time.Sleep), got: %v", err)
	}
}
