// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rimsky-ai/rimsky-core/test/support/imagetag"
)

const (
	bootMaxAttempts  = 3
	bootRetryBackoff = 5 * time.Second
)

func Run(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*testcontainers.DockerContainer, error) {
	return runWithRetry(ctx, img, opts...)
}

func runWithRetry(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*testcontainers.DockerContainer, error) {
	return runContainerWithRetry(ctx,
		func(ctx context.Context) (*testcontainers.DockerContainer, error) {
			c, err := testcontainers.Run(ctx, img, opts...)
			if err != nil && c != nil {
				terminateBootFailure(ctx, c, err)
			}
			return c, err
		},
		func(err error) (error, bool) {
			if imagetag.IsMissingLocalImage(img, err) {
				return imagetag.MissingImageError(img, err), true
			}
			return nil, false
		},
	)
}

func runPostgresWithRetry(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*pgmodule.PostgresContainer, error) {
	return runContainerWithRetry(ctx,
		func(ctx context.Context) (*pgmodule.PostgresContainer, error) {
			c, err := pgmodule.Run(ctx, img, opts...)
			if err != nil && c != nil {
				terminateBootFailure(ctx, c, err)
			}
			return c, err
		},
		nil,
	)
}

func runContainerWithRetry[T any](
	ctx context.Context,
	run func(context.Context) (T, error),
	failFast func(error) (error, bool),
) (T, error) {
	var lastErr error
	for attempt := 1; attempt <= bootMaxAttempts; attempt++ {
		c, err := run(ctx)
		if err == nil {
			return c, nil
		}
		if failFast != nil {
			if ffErr, abort := failFast(err); abort {
				var zero T
				return zero, ffErr
			}
		}
		lastErr = err
		if attempt < bootMaxAttempts {
			time.Sleep(time.Duration(attempt) * bootRetryBackoff)
		}
	}
	var zero T
	return zero, fmt.Errorf("after %d boot attempts: %w", bootMaxAttempts, lastErr)
}

func terminateBootFailure(ctx context.Context, c testcontainers.Container, bootErr error) {
	if c == nil || bootErr == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = c.Terminate(termCtx)
}
