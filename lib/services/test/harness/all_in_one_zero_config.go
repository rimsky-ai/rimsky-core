// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const AllInOneStateVolume = "/var/lib/rimsky"

type ZeroConfigStack struct {
	Endpoint RimskyEndpoint

	t         testing.TB
	container testcontainers.Container
}

// @story: local-orchestrator-zero-config
func StartAllInOneZeroConfig(ctx context.Context, t testing.TB, hostStateDir string) *ZeroConfigStack {
	t.Helper()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("8080/tcp").WithStartupTimeout(120 * time.Second),
		),
	}
	if hostStateDir != "" {
		opts = append(opts, testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.Binds = append(hc.Binds, hostStateDir+":"+AllInOneStateVolume+":rw")
		}))
	}
	c, err := runWithRetry(ctx, ImageRef(rimskyAllImage), opts...)
	if err != nil {
		t.Fatalf("harness: start zero-config rimsky-all-in-one: %v", err)
	}
	s := &ZeroConfigStack{t: t, container: c}
	t.Cleanup(func() { s.Stop(context.Background()) })

	hostIP, err := c.Host(ctx)
	if err != nil {
		dumpLogsForFailure(t, "rimsky-all-in-one", c)
		t.Fatalf("harness: zero-config all-in-one host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "8080")
	if err != nil {
		dumpLogsForFailure(t, "rimsky-all-in-one", c)
		t.Fatalf("harness: zero-config all-in-one mapped port: %v", err)
	}
	s.Endpoint = RimskyEndpoint{BaseURL: fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())}
	if err := waitForHealth(ctx, s.Endpoint.BaseURL); err != nil {
		dumpLogsForFailure(t, "rimsky-all-in-one", c)
		t.Fatalf("harness: zero-config all-in-one /health did not return 200: %v", err)
	}
	return s
}

func (s *ZeroConfigStack) Stop(ctx context.Context) {
	s.t.Helper()
	if s.container == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = s.container.Terminate(termCtx)
	s.container = nil
}
