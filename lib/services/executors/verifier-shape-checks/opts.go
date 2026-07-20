// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	return Opts{
		Host:     envOr("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_HOST", "0.0.0.0"),
		Port:     agentport.Resolve("RIMSKY_EXECUTOR_VERIFIER_SHAPE_CHECKS_PORT", 9095),
		StubMode: os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1",
	}, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
