// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config bundles the operator-supplied configuration for the openlineage
// subscriber. Loaded from env vars at startup; per the 2026-05-15
// data-platform-extensions plan §Section K the subscriber is a
// standalone binary, so env-only config keeps the deployment surface
// minimal.
type Config struct {
	// RimskyDSN is the Postgres connection string for the rimsky-side
	// projection (`table:rimsky_lineage`). pgx is allowed under the
	// `subscribers/` path per the `.golangci.yml` `pgx-isolation`
	// allowlist (extended in this dispatch).
	RimskyDSN string
	// StateDSN is the Postgres / SQLite connection string for the
	// subscriber's own cursor state. May equal RimskyDSN; ops typically
	// runs them in distinct schemas so the cursor survives a rimsky
	// dev-DB nuke.
	StateDSN string
	// BackendURL is the OpenLineage HTTP receiver. The subscriber POSTs
	// OpenLineage 1.x JSON envelopes to `{BackendURL}/api/v1/lineage`
	// (Marquez convention). Empty → subscriber logs and exits.
	BackendURL string
	// Namespace is the OpenLineage namespace stamped on every emitted
	// event. Operators typically pick one namespace per rimsky
	// deployment (e.g. `analytics_production`).
	Namespace string
	// PollInterval governs how often the subscriber polls
	// `table:rimsky_lineage` for new rows. Default 5s.
	PollInterval time.Duration
	// BatchSize caps the number of new rows processed per poll. Bounds
	// memory + emitter latency; default 200.
	BatchSize int
}

// LoadConfig reads env vars and returns a Config. Errors describe the
// specific missing or malformed variable so deployment scripts can show
// precise diagnostics.
func LoadConfig() (Config, error) {
	rimsky := os.Getenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN")
	if rimsky == "" {
		return Config{}, fmt.Errorf("env RIMSKY_OPENLINEAGE_RIMSKY_DSN required")
	}
	state := os.Getenv("RIMSKY_OPENLINEAGE_STATE_DSN")
	if state == "" {
		// Default to the rimsky DB (table `rimsky_openlineage_cursor`).
		state = rimsky
	}
	backend := os.Getenv("RIMSKY_OPENLINEAGE_BACKEND_URL")
	namespace := os.Getenv("RIMSKY_OPENLINEAGE_NAMESPACE")
	if namespace == "" {
		namespace = "rimsky"
	}
	poll := 5 * time.Second
	if v := os.Getenv("RIMSKY_OPENLINEAGE_POLL_INTERVAL"); v != "" {
		p, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RIMSKY_OPENLINEAGE_POLL_INTERVAL: %w", err)
		}
		poll = p
	}
	batch := 200
	if v := os.Getenv("RIMSKY_OPENLINEAGE_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("RIMSKY_OPENLINEAGE_BATCH_SIZE: must be a positive integer")
		}
		batch = n
	}
	return Config{
		RimskyDSN:    rimsky,
		StateDSN:     state,
		BackendURL:   backend,
		Namespace:    namespace,
		PollInterval: poll,
		BatchSize:    batch,
	}, nil
}
