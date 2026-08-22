// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/ports"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

type Config struct {
	GRPCPort          int
	PeerGRPCPort      int
	ControlAPIURL     string
	ControlAPIToken   string
	ControlAPICAPath  string
	LogLevel          string
	TLSCertPath       string
	TLSKeyPath        string
	LocalCAPath       string
	Insecure          bool
	SpawnReadyTimeout time.Duration
	ReapTimeout       time.Duration
}

// @decision: default-port-allocation
func LoadConfig() Config {
	return Config{
		GRPCPort:         envInt("RIMSKY_PROXY_GRPC_PORT", ports.HostAgentProxyAgentFacing),
		PeerGRPCPort:     envInt("RIMSKY_PROXY_PEER_GRPC_PORT", ports.HostAgentProxyPeerFacing),
		ControlAPIURL:    trimTrailingSlash(os.Getenv("RIMSKY_CONTROL_API_URL")),
		ControlAPIToken:  os.Getenv("RIMSKY_CONTROL_API_TOKEN"),
		ControlAPICAPath: os.Getenv(enroll.EnvControlAPICA),
		LogLevel:         envOr("RIMSKY_LOG_LEVEL", "info"),
		TLSCertPath:      os.Getenv("RIMSKY_PROXY_TLS_CERT"),
		TLSKeyPath:       os.Getenv("RIMSKY_PROXY_TLS_KEY"),
		LocalCAPath:      os.Getenv(envLocalCAFile),
		// @decision: host-agent-proxy-tls
		Insecure:          envBool(envInsecureHop),
		SpawnReadyTimeout: 30 * time.Second,
		ReapTimeout:       45 * time.Second,
	}
}

// @decision: host-agent-proxy-tls
const envInsecureHop = "RIMSKY_HOST_AGENT_INSECURE"

const envLocalCAFile = "RIMSKY_PROXY_LOCAL_CA_FILE"

func envBool(key string) bool {
	switch strings.TrimSpace(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
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
