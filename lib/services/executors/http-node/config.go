// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"strconv"
)

type Config struct {
	Host            string
	GRPCPort        int
	HTTPPort        int
	TimeoutMs       int
	MaxBodyBytes    int
	StubMode        bool
	HTTPBridgeURL   string
	ErrorClassField string
}

const DefaultErrorClassField = "error_class"

func LoadConfig() Config {
	cfg := Config{Host: env("RIMSKY_EXECUTOR_HTTP_NODE_HOST", "0.0.0.0")}
	cfg.GRPCPort = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_PORT", "9091"))
	cfg.HTTPPort = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT", strconv.Itoa(cfg.GRPCPort+1)))
	cfg.TimeoutMs = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", "60000"))
	cfg.MaxBodyBytes = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "10485760"))
	cfg.StubMode = env("RIMSKY_EXECUTOR_STUB_MODE", "0") == "1"
	cfg.HTTPBridgeURL = env("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL", "")
	cfg.ErrorClassField = env("RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD", DefaultErrorClassField)
	return cfg
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
