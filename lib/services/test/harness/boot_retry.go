// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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
	return runContainerWithRetry(ctx, img,
		func(ctx context.Context) (*testcontainers.DockerContainer, error) {
			c, err := testcontainers.Run(ctx, img, opts...)
			if err != nil && c != nil {
				terminateBootFailure(ctx, img, c, err)
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
	return runContainerWithRetry(ctx, img,
		func(ctx context.Context) (*pgmodule.PostgresContainer, error) {
			c, err := pgmodule.Run(ctx, img, opts...)
			if err != nil && c != nil {
				terminateBootFailure(ctx, img, c, err)
			}
			return c, err
		},
		nil,
	)
}

func runContainerWithRetry[T any](
	ctx context.Context,
	img string,
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
		slog.Warn("harness: container boot attempt failed",
			"image", img, "attempt", attempt, "max_attempts", bootMaxAttempts, "error", err.Error())
		if attempt < bootMaxAttempts {
			time.Sleep(time.Duration(attempt) * bootRetryBackoff)
		}
	}
	var zero T
	return zero, fmt.Errorf("after %d boot attempts: %w", bootMaxAttempts, lastErr)
}

func terminateBootFailure(ctx context.Context, img string, c testcontainers.Container, bootErr error) {
	if c == nil || bootErr == nil {
		return
	}
	logCtx, logCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if logs, logErr := c.Logs(logCtx); logErr == nil {
		if tail, readErr := io.ReadAll(logs); readErr == nil && len(tail) > 0 {
			const maxTail = 4096
			if len(tail) > maxTail {
				tail = tail[len(tail)-maxTail:]
			}
			slog.Warn("harness: container logs at boot failure", "image", img, "logs_tail", string(tail))
		}
		_ = logs.Close()
	}
	logCancel()
	termCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = c.Terminate(termCtx)
}

func rereadableContainerFile(content, containerPath string, mode int64) testcontainers.ContainerFile {
	f, err := os.CreateTemp("", "rimsky-harness-container-file-*")
	if err != nil {
		panic(fmt.Sprintf("harness: stage container file for %s: %v", containerPath, err))
	}
	if _, err := f.WriteString(content); err != nil {
		panic(fmt.Sprintf("harness: stage container file for %s: %v", containerPath, err))
	}
	if err := f.Close(); err != nil {
		panic(fmt.Sprintf("harness: stage container file for %s: %v", containerPath, err))
	}
	return testcontainers.ContainerFile{
		HostFilePath:      f.Name(),
		ContainerFilePath: containerPath,
		FileMode:          mode,
	}
}
