// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: host-daemon-proxy-enrollment
package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

func controlAPIHTTPClient(cfg Config, timeout time.Duration) (*http.Client, error) {
	if cfg.ControlAPICAPath == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	if !strings.HasPrefix(cfg.ControlAPIURL, "https://") {
		return nil, fmt.Errorf("%s is set but %s=%q is not https — a pinned CA root cannot secure a plaintext control-API hop",
			enroll.EnvControlAPICA, "RIMSKY_CONTROL_API_URL", cfg.ControlAPIURL)
	}
	pool, err := enroll.CAPoolFromFile(cfg.ControlAPICAPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", enroll.EnvControlAPICA, err)
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: enroll.PinnedTLSConfig(pool)},
	}, nil
}
