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
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const hostForwarderPort = 9500

func StartHostForwarderOnNetwork(ctx context.Context, t testing.TB, networkName, alias string, hostPort int) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	c, err := runWithRetry(ctx, "alpine/socat:1.8.0.3",
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithCmd(
			fmt.Sprintf("tcp-listen:%d,fork,reuseaddr", hostForwarderPort),
			fmt.Sprintf("tcp-connect:host.docker.internal:%d", hostPort),
		),
		testcontainers.WithExposedPorts(fmt.Sprintf("%d/tcp", hostForwarderPort)),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort(fmt.Sprintf("%d/tcp", hostForwarderPort)).WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start host forwarder %s -> host:%d: %v", alias, hostPort, err)
	}
	t.Cleanup(func() {
		//nolint:testwallclock-pacing the teardown discards the terminate error, so no verdict reads this grace
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return fmt.Sprintf("%s:%d", uniqueAlias, hostForwarderPort)
}
