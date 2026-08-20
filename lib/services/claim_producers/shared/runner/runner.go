// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

func Fatal(serviceName string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
	os.Exit(1)
}

func Listen(host string, grpcPort, httpPort, adminPort int) (grpcLis, httpLis, adminLis net.Listener, err error) {
	grpcLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, grpcPort))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("grpc listen: %w", err)
	}
	httpLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, httpPort))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("http listen: %w", err)
	}
	if adminPort > 0 {
		adminLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, adminPort))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("admin listen: %w", err)
		}
	}
	return grpcLis, httpLis, adminLis, nil
}

// @decision: graceful-shutdown
func SignalContext() (context.Context, context.CancelFunc) {
	return serverkit.ShutdownContext(context.Background(), slog.Default())
}
