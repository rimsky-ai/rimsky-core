// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import (
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

type Opts struct {
	Host     string
	Port     int
	StubMode bool
}

func LoadOptsFromEnv() (Opts, error) {
	port, err := agentport.Resolve("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT", 9095)
	if err != nil {
		return Opts{}, err
	}
	return Opts{
		Host:     envOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_HOST", "0.0.0.0"),
		Port:     port,
		StubMode: os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1",
	}, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
