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
	ProxyURL           string
	APIKey             string
	ListenAddr         string
	AllowPaths         []string
	AgentLabel         string
	LogLevel           string
	HeartbeatInterval  time.Duration
	ReapGracePeriod    time.Duration
	RegisterAckTimeout time.Duration
	TLSEnabled         bool
	TLSCAPath          string
	// @story: host-agent-control-plane
	StatusFile string
	// @concept: anonymous-mode
	RoutingLabel string
	IdentityFile string
}

func LoadConfigFromEnv() (Config, error) {
	heartbeat, err := envDurationSec("RIMSKY_AGENT_HEARTBEAT_SEC", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	reapGrace, err := envDurationSec("RIMSKY_AGENT_REAP_GRACE_SEC", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	registerAckTimeout, err := envDurationSec("RIMSKY_AGENT_REGISTER_ACK_TIMEOUT_SEC", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	return Config{
		ProxyURL:           os.Getenv("RIMSKY_HOST_AGENT_PROXY_URL"),
		APIKey:             os.Getenv("RIMSKY_API_KEY"),
		ListenAddr:         os.Getenv("RIMSKY_AGENT_LISTEN"),
		AgentLabel:         envOr("RIMSKY_AGENT_LABEL", defaultAgentLabel()),
		LogLevel:           envOr("RIMSKY_LOG_LEVEL", "info"),
		HeartbeatInterval:  heartbeat,
		ReapGracePeriod:    reapGrace,
		RegisterAckTimeout: registerAckTimeout,
		TLSEnabled:         envBool("RIMSKY_AGENT_TLS"),
		TLSCAPath:          os.Getenv("RIMSKY_AGENT_TLS_CA"),
		StatusFile:         os.Getenv("RIMSKY_AGENT_STATUS_FILE"),
		RoutingLabel:       os.Getenv(agentRoutingLabelEnvVar),
		IdentityFile:       os.Getenv("RIMSKY_AGENT_IDENTITY_FILE"),
	}, nil
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
	if c.RegisterAckTimeout <= 0 {
		c.RegisterAckTimeout = 15 * time.Second
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

func envBool(key string) bool {
	switch strings.TrimSpace(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envDurationSec(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	var secs int
	if _, err := fmt.Sscanf(v, "%d", &secs); err != nil {
		return 0, fmt.Errorf("%s: invalid integer seconds value %q", key, v)
	}
	if secs <= 0 {
		return 0, fmt.Errorf("%s: must be a positive number of seconds, got %d", key, secs)
	}
	return time.Duration(secs) * time.Second, nil
}
