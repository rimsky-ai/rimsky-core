// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"strconv"
	"time"
)

// Config is the proxy's startup configuration, read once from the
// environment. Configuration-as-visible-object per the cold-read
// conventions: every env read lives here, not scattered through the
// handlers.
type Config struct {
	// GRPCPort serves HostAgent.Connect (agent-facing) AND the rimsky
	// service protocols (supervisor-facing) on the same port.
	GRPCPort int
	// ControlAPIURL is the base URL for the GET /instances/{id} cache-miss
	// fallback (no trailing slash).
	ControlAPIURL string
	// ControlAPIToken is the api-key the proxy presents to control-api as
	// itself when fetching instance state on a binding-cache miss.
	ControlAPIToken string
	// LogLevel is debug|info|warn|error (default info).
	LogLevel string
	// SpawnReadyTimeout bounds the wait for a SpawnAck after issuing a
	// Spawn frame. On timeout the dispatch fails with spawn_failed.
	SpawnReadyTimeout time.Duration
	// ReapTimeout bounds the wait for a Reaped ack after issuing a Reap
	// frame during run-scope teardown.
	ReapTimeout time.Duration
}

// LoadConfig reads the proxy configuration from the environment.
//
//	RIMSKY_PROXY_GRPC_PORT   optional; default 9090.
//	RIMSKY_CONTROL_API_URL   optional; base URL for the instance fallback.
//	RIMSKY_CONTROL_API_TOKEN optional; bearer token for the fallback.
//	RIMSKY_LOG_LEVEL         optional; debug|info|warn|error (default info).
func LoadConfig() Config {
	return Config{
		GRPCPort:          envInt("RIMSKY_PROXY_GRPC_PORT", 9090),
		ControlAPIURL:     trimTrailingSlash(os.Getenv("RIMSKY_CONTROL_API_URL")),
		ControlAPIToken:   os.Getenv("RIMSKY_CONTROL_API_TOKEN"),
		LogLevel:          envOr("RIMSKY_LOG_LEVEL", "info"),
		SpawnReadyTimeout: 30 * time.Second,
		ReapTimeout:       45 * time.Second,
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
