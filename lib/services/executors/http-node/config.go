// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"fmt"
	"os"
	"strconv"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

type Opts struct {
	Host            string
	GRPCPort        int
	HTTPPort        int
	MaxBodyBytes    int
	StubMode        bool
	HTTPBridgeURL   string
	ErrorClassField string
	Egress          egress.Guard
}

const DefaultErrorClassField = "error_class"

// @decision: default-port-allocation
const (
	defaultGRPCPort = 9091
	defaultHTTPPort = 9092
)

func LoadOptsFromEnv() (Opts, error) {
	opts := Opts{Host: envOr("RIMSKY_EXECUTOR_HOST", "0.0.0.0")}
	grpcPort, err := agentport.Resolve("RIMSKY_EXECUTOR_PORT_GRPC", defaultGRPCPort)
	if err != nil {
		return Opts{}, err
	}
	opts.GRPCPort = grpcPort
	httpFallback := grpcPort + 1
	if opts.HTTPPort, err = atoi(envOr("RIMSKY_EXECUTOR_PORT_HTTP", strconv.Itoa(httpFallback)), "RIMSKY_EXECUTOR_PORT_HTTP"); err != nil {
		return Opts{}, err
	}
	if opts.MaxBodyBytes, err = atoi(envOr("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "10485760"), "RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES"); err != nil {
		return Opts{}, err
	}
	opts.StubMode = envOr("RIMSKY_EXECUTOR_STUB_MODE", "0") == "1"
	opts.HTTPBridgeURL = envOr("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL", "")
	opts.ErrorClassField = envOr("RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD", DefaultErrorClassField)
	// @decision: destination-allowlists-default-closed
	guard, err := egress.NewGuardFromEnv("RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST")
	if err != nil {
		return Opts{}, err
	}
	opts.Egress = guard
	return opts, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid integer", name, s)
	}
	return n, nil
}
