// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package hostdaemon

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
	DaemonLabel        string
	LogLevel           string
	HeartbeatInterval  time.Duration
	ReapGracePeriod    time.Duration
	RegisterAckTimeout time.Duration
	// @decision: host-daemon-proxy-tls
	Insecure  bool
	TLSCAPath string
	// @story: host-daemon-control-plane
	StatusFile string
	// @concept: anonymous-mode
	RoutingLabel string
	IdentityFile string
}

func LoadConfigFromEnv() (Config, error) {
	heartbeat, err := envDurationSec("RIMSKY_DAEMON_HEARTBEAT_SEC", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	reapGrace, err := envDurationSec("RIMSKY_DAEMON_REAP_GRACE_SEC", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	registerAckTimeout, err := envDurationSec("RIMSKY_DAEMON_REGISTER_ACK_TIMEOUT_SEC", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	return Config{
		ProxyURL:           os.Getenv("RIMSKY_HOST_DAEMON_PROXY_URL"),
		APIKey:             os.Getenv("RIMSKY_API_KEY"),
		ListenAddr:         os.Getenv("RIMSKY_DAEMON_LISTEN"),
		AllowPaths:         splitCommaNonEmpty(os.Getenv(allowPathsEnvVar)),
		DaemonLabel:        envOr("RIMSKY_DAEMON_LABEL", defaultDaemonLabel()),
		LogLevel:           envOr("RIMSKY_LOG_LEVEL", "info"),
		HeartbeatInterval:  heartbeat,
		ReapGracePeriod:    reapGrace,
		RegisterAckTimeout: registerAckTimeout,
		Insecure:           envBool(EnvInsecureHop),
		TLSCAPath:          os.Getenv("RIMSKY_DAEMON_TLS_CA"),
		StatusFile:         os.Getenv("RIMSKY_DAEMON_STATUS_FILE"),
		RoutingLabel:       os.Getenv(daemonRoutingLabelEnvVar),
		IdentityFile:       os.Getenv(IdentityFileEnvVar),
	}, nil
}

func (c Config) withDefaults() Config {
	if c.DaemonLabel == "" {
		c.DaemonLabel = defaultDaemonLabel()
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

func defaultDaemonLabel() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

const allowPathsEnvVar = "RIMSKY_DAEMON_ALLOW_PATHS"

// @decision: host-daemon-proxy-tls
const EnvInsecureHop = "RIMSKY_HOST_DAEMON_INSECURE"

func splitCommaNonEmpty(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
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
