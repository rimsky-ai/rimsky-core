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
	var lastErr error
	for attempt := 1; attempt <= bootMaxAttempts; attempt++ {
		c, err := testcontainers.Run(ctx, img, opts...)
		if err == nil {
			return c, nil
		}
		if c != nil {
			terminateBootFailure(ctx, c, err)
		}
		if imagetag.IsMissingLocalImage(img, err) {
			return nil, imagetag.MissingImageError(img, err)
		}
		lastErr = err
		if attempt < bootMaxAttempts {
			time.Sleep(time.Duration(attempt) * bootRetryBackoff)
		}
	}
	return nil, fmt.Errorf("after %d boot attempts: %w", bootMaxAttempts, lastErr)
}

func runPostgresWithRetry(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*pgmodule.PostgresContainer, error) {
	var lastErr error
	for attempt := 1; attempt <= bootMaxAttempts; attempt++ {
		c, err := pgmodule.Run(ctx, img, opts...)
		if err == nil {
			return c, nil
		}
		if c != nil {
			terminateBootFailure(ctx, c, err)
		}
		lastErr = err
		if attempt < bootMaxAttempts {
			time.Sleep(time.Duration(attempt) * bootRetryBackoff)
		}
	}
	return nil, fmt.Errorf("after %d boot attempts: %w", bootMaxAttempts, lastErr)
}

func terminateBootFailure(ctx context.Context, c testcontainers.Container, bootErr error) {
	if c == nil || bootErr == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = c.Terminate(termCtx)
}
