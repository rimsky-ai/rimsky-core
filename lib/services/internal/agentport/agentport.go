// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package agentport

import (
	"os"
	"strconv"
)

const EnvVar = "RIMSKY_AGENT_PORT"

func Resolve(fallbackEnvVar string, def int) int {
	return Override(intEnvOr(fallbackEnvVar, def))
}

func Override(fallback int) int {
	if v, ok := intEnv(EnvVar); ok {
		return v
	}
	return fallback
}

func intEnvOr(name string, def int) int {
	if v, ok := intEnv(name); ok {
		return v
	}
	return def
}

func intEnv(name string) (int, bool) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}
