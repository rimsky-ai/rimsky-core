// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
)

type ExitedContainer struct {
	ExitCode int
	Logs     string
}

func RunImageToExit(
	ctx context.Context, t testing.TB, name, image, networkName string, env map[string]string,
) ExitedContainer {
	t.Helper()
	name = fmt.Sprintf("%s-%d", name, nextAliasSuffix())
	c, err := runWithRetry(ctx, ImageRef(image),
		tcnet.WithNetworkName([]string{name}, networkName),
		testcontainers.WithEnv(env),
	)
	if err != nil {
		t.Fatalf("harness: start %s (expected to exit on its own): %v", name, err)
	}
	t.Cleanup(func() {
		//nolint:testwallclock-pacing the teardown discards the terminate error, so no verdict reads this grace
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	exitCode := waitForContainerExit(ctx, t, name, c)
	return ExitedContainer{ExitCode: exitCode, Logs: readContainerLogs(t, name, c)}
}

func readContainerLogs(t testing.TB, name string, c testcontainers.Container) string {
	t.Helper()
	rc, err := c.Logs(context.Background())
	if err != nil {
		t.Fatalf("harness: read %s logs: %v", name, err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("harness: read %s logs: %v", name, err)
	}
	return string(out)
}
