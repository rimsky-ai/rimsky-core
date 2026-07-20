// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
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
	opts.GRPCPort = agentport.Resolve("RIMSKY_EXECUTOR_HTTP_NODE_PORT", 9091)
	opts.HTTPPort = atoiOr("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT", opts.GRPCPort+1)
	opts.TimeoutMs = atoiOr("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", 60000)
	opts.MaxBodyBytes = atoiOr("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", 10485760)
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

func atoiOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
