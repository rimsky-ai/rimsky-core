// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	GRPCPort          int
	ControlAPIURL     string
	ControlAPIToken   string
	LogLevel          string
	TLSCertPath       string
	TLSKeyPath        string
	SpawnReadyTimeout time.Duration
	ReapTimeout       time.Duration
}

func LoadConfig() Config {
	return Config{
		GRPCPort:          envInt("RIMSKY_PROXY_GRPC_PORT", 9090),
		ControlAPIURL:     trimTrailingSlash(os.Getenv("RIMSKY_CONTROL_API_URL")),
		ControlAPIToken:   os.Getenv("RIMSKY_CONTROL_API_TOKEN"),
		LogLevel:          envOr("RIMSKY_LOG_LEVEL", "info"),
		TLSCertPath:       os.Getenv("RIMSKY_PROXY_TLS_CERT"),
		TLSKeyPath:        os.Getenv("RIMSKY_PROXY_TLS_KEY"),
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
