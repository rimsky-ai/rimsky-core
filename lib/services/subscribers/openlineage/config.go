// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	RimskyDSN    string
	StateDSN     string
	BackendURL   string
	Namespace    string
	PollInterval time.Duration
	BatchSize    int
}

func LoadConfig() (Config, error) {
	rimsky := os.Getenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN")
	if rimsky == "" {
		return Config{}, fmt.Errorf("env RIMSKY_OPENLINEAGE_RIMSKY_DSN required")
	}
	state := os.Getenv("RIMSKY_OPENLINEAGE_STATE_DSN")
	if state == "" {
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
