package main

import (
	"os"
	"strconv"
)

// Config is the env-driven configuration for the http-node executor.
// All fields are explicit; there is no scattered env lookup elsewhere.
type Config struct {
	Host         string
	GRPCPort     int
	HTTPPort     int
	TimeoutMs    int
	MaxBodyBytes int
	StubMode     bool
	// HTTPBridgeURL is the dashboard-visible HTTP base URL the
	// executor advertises in ObservabilityCapabilities.http_bridge_url.
	// Operators set this to the externally-reachable URL of the HTTP
	// listener (e.g. "http://http-node:9092" in the compose stack).
	// When empty, the dashboard's HTTP proxy falls back to the
	// dispatch endpoint and observability streaming may be unreachable.
	HTTPBridgeURL string
}

// LoadConfig reads the RIMSKY_EXECUTOR_HTTP_NODE_* env vars and returns a
// populated Config, applying defaults for any unset value.
func LoadConfig() Config {
	cfg := Config{Host: env("RIMSKY_EXECUTOR_HTTP_NODE_HOST", "0.0.0.0")}
	cfg.GRPCPort = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_PORT", "9091"))
	cfg.HTTPPort = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT", strconv.Itoa(cfg.GRPCPort+1)))
	cfg.TimeoutMs = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", "60000"))
	cfg.MaxBodyBytes = atoi(env("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "10485760"))
	cfg.StubMode = env("RIMSKY_EXECUTOR_STUB_MODE", "0") == "1"
	cfg.HTTPBridgeURL = env("RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL", "")
	return cfg
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
