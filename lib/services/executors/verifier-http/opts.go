// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifierhttp

import (
	"os"
	"strconv"
)

type Opts struct {
	Host     string
	Port     int
	StubMode bool
}

func LoadOptsFromEnv() (Opts, error) {
	return Opts{
		Host:     envOr("RIMSKY_EXECUTOR_VERIFIER_HTTP_HOST", "0.0.0.0"),
		Port:     atoiOr("RIMSKY_EXECUTOR_VERIFIER_HTTP_PORT", 9096),
		StubMode: os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1",
	}, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
