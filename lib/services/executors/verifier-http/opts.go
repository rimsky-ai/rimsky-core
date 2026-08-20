// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import (
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"
)

// @decision: default-port-allocation
const defaultGRPCPort = 9096

type Opts struct {
	Host     string
	Port     int
	StubMode bool

	// @concept: peer-auth
	Egress egress.Guard
}

func LoadOptsFromEnv() (Opts, error) {
	port, err := agentport.Resolve("RIMSKY_EXECUTOR_PORT_GRPC", defaultGRPCPort)
	if err != nil {
		return Opts{}, err
	}
	// @decision: destination-allowlists-default-closed
	guard, err := egress.NewGuardFromEnv("RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST")
	if err != nil {
		return Opts{}, err
	}
	return Opts{
		Host:     envOr("RIMSKY_EXECUTOR_HOST", "0.0.0.0"),
		Port:     port,
		StubMode: os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1",
		Egress:   guard,
	}, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
