// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	TimeoutMs       int
	MaxBodyBytes    int
	StubMode        bool
	HTTPBridgeURL   string
	ErrorClassField string
	Egress          egress.Guard
}

const DefaultErrorClassField = "error_class"

func LoadOptsFromEnv() (Opts, error) {
	opts := Opts{Host: envOr("RIMSKY_EXECUTOR_HTTP_NODE_HOST", "0.0.0.0")}
	grpcPort, err := agentport.Resolve("RIMSKY_EXECUTOR_HTTP_NODE_PORT", 9091)
	if err != nil {
		return Opts{}, err
	}
	opts.GRPCPort = grpcPort
	if opts.HTTPPort, err = atoi(envOr("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT", strconv.Itoa(opts.GRPCPort+1)), "RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT"); err != nil {
		return Opts{}, err
	}
	if opts.TimeoutMs, err = atoi(envOr("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", "60000"), "RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS"); err != nil {
		return Opts{}, err
	}
	if opts.MaxBodyBytes, err = atoi(envOr("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "10485760"), "RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES"); err != nil {
		return Opts{}, err
	}
	opts.StubMode = envOr("RIMSKY_EXECUTOR_STUB_MODE", "0") == "1"
	opts.HTTPBridgeURL = envOr("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL", "")
	opts.ErrorClassField = envOr("RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD", DefaultErrorClassField)
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
