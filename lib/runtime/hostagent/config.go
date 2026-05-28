// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package hostagent implements the rimsky-host-agent daemon: a long-lived
// process on a dev machine that dials the rimsky-host-agent-proxy outbound,
// receives Spawn/Dispatch/Reap frames, exec()s local binaries, tunnels
// their gRPC streams + local HTTP callbacks back through the bidi stream,
// and reaps the children on signal. It is the agent end of the
// HostAgent.Connect protocol whose proxy end lives in
// cmd/rimsky-host-agent-proxy.
//
// @concept: host-agent
package hostagent

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the host-agent's startup configuration. Configuration-as-
// visible-object per the cold-read conventions: every env read lives in
// LoadConfigFromEnv, not scattered through the daemon.
type Config struct {
	// RimskyURL is the proxy's agent-facing gRPC endpoint the daemon dials
	// outbound (host:port). The daemon dials with insecure transport in v1.
	RimskyURL string
	// APIKey authenticates the agent to the proxy in the Register frame.
	APIKey string
	// ListenAddr is the local HTTP listener address. Empty → an OS-assigned
	// ephemeral port on 127.0.0.1. The bound address is reported to the
	// proxy as the Register.local_callback_base_url so the proxy can rewrite
	// callback URLs onto this listener.
	ListenAddr string
	// AllowPaths is a set of filepath.Match globs. When non-empty, a Spawn
	// whose binding.path matches none of them is rejected. Empty → permissive
	// (the default trust posture: holding the api-key authorizes any spawn).
	AllowPaths []string
	// AgentLabel disambiguates multiple agents for the same api-key. Default
	// is "<hostname>-<pid>".
	AgentLabel string
	// LogLevel is debug|info|warn|error (default info).
	LogLevel string
	// HeartbeatInterval is the cadence of HostAgentHeartbeat frames on the
	// stream. Default 10s.
	HeartbeatInterval time.Duration
	// ReapGracePeriod bounds how long orphaned children are given to exit
	// after a stream close before they are SIGKILLed. Default 30s.
	ReapGracePeriod time.Duration
}

// LoadConfigFromEnv reads the host-agent configuration from the environment.
//
//	RIMSKY_URL                 proxy agent-facing endpoint (host:port).
//	RIMSKY_API_KEY             api-key presented in Register.
//	RIMSKY_AGENT_LISTEN        optional; local HTTP listener addr.
//	RIMSKY_AGENT_LABEL         optional; defaults to "<hostname>-<pid>".
//	RIMSKY_LOG_LEVEL           optional; debug|info|warn|error (default info).
//	RIMSKY_AGENT_HEARTBEAT_SEC optional; heartbeat cadence seconds (default 10).
//	RIMSKY_AGENT_REAP_GRACE_SEC optional; reap grace seconds (default 30).
func LoadConfigFromEnv() Config {
	return Config{
		RimskyURL:         os.Getenv("RIMSKY_URL"),
		APIKey:            os.Getenv("RIMSKY_API_KEY"),
		ListenAddr:        os.Getenv("RIMSKY_AGENT_LISTEN"),
		AgentLabel:        envOr("RIMSKY_AGENT_LABEL", defaultAgentLabel()),
		LogLevel:          envOr("RIMSKY_LOG_LEVEL", "info"),
		HeartbeatInterval: envDurationSec("RIMSKY_AGENT_HEARTBEAT_SEC", 10*time.Second),
		ReapGracePeriod:   envDurationSec("RIMSKY_AGENT_REAP_GRACE_SEC", 30*time.Second),
	}
}

// withDefaults fills in zero-valued fields with their documented defaults so
// callers that build a Config by hand (the CLI subcommand, tests) get the
// same behavior as LoadConfigFromEnv.
func (c Config) withDefaults() Config {
	if c.AgentLabel == "" {
		c.AgentLabel = defaultAgentLabel()
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 10 * time.Second
	}
	if c.ReapGracePeriod <= 0 {
		c.ReapGracePeriod = 30 * time.Second
	}
	return c
}

// defaultAgentLabel is "<hostname>-<pid>"; hostname falls back to "unknown".
func defaultAgentLabel() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationSec(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	var secs int
	if _, err := fmt.Sscanf(v, "%d", &secs); err != nil || secs <= 0 {
		return def
	}
	return time.Duration(secs) * time.Second
}
