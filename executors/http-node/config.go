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
	return cfg
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
