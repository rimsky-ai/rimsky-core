// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package agentport

import (
	"fmt"
	"os"
	"strconv"
)

const EnvVar = "RIMSKY_AGENT_PORT"

func Resolve(fallbackEnvVar string, def int) (int, error) {
	base, err := intEnvOr(fallbackEnvVar, def)
	if err != nil {
		return 0, err
	}
	return Override(base)
}

func Override(fallback int) (int, error) {
	v, ok, err := intEnv(EnvVar)
	if err != nil {
		return 0, err
	}
	if ok {
		return v, nil
	}
	return fallback, nil
}

func intEnvOr(name string, def int) (int, error) {
	v, ok, err := intEnv(name)
	if err != nil {
		return 0, err
	}
	if ok {
		return v, nil
	}
	return def, nil
}

func intEnv(name string) (int, bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%s=%q is not a valid integer port", name, raw)
	}
	return n, true, nil
}
