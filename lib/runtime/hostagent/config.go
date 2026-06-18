// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	RimskyURL string
	APIKey string
	ListenAddr string
	AllowPaths []string
	AgentLabel string
	LogLevel string
	HeartbeatInterval time.Duration
	ReapGracePeriod time.Duration
	// @story: host-agent-control-plane — the falsifier "status reports
	StatusFile string
}

func LoadConfigFromEnv() Config {
	return Config{
		RimskyURL:         os.Getenv("RIMSKY_URL"),
		APIKey:            os.Getenv("RIMSKY_API_KEY"),
		ListenAddr:        os.Getenv("RIMSKY_AGENT_LISTEN"),
		AgentLabel:        envOr("RIMSKY_AGENT_LABEL", defaultAgentLabel()),
		LogLevel:          envOr("RIMSKY_LOG_LEVEL", "info"),
		HeartbeatInterval: envDurationSec("RIMSKY_AGENT_HEARTBEAT_SEC", 10*time.Second),
		ReapGracePeriod:   envDurationSec("RIMSKY_AGENT_REAP_GRACE_SEC", 30*time.Second),
		StatusFile:        os.Getenv("RIMSKY_AGENT_STATUS_FILE"),
	}
}

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
