// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/internal/bundledwire"
	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type RoleStack struct {
	unified   *launch.UnifiedStack
	endpoint  string
	driver    persistence.Database
	drainOnce sync.Once
}

type RoleFailure = launch.RoleFailure

// @decision: migration-direct
func MigratePersistence(ctx context.Context, logger *slog.Logger, configPath string) (persistence.Database, config.RimskyConfig, error) {
	cfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		return nil, config.RimskyConfig{}, fmt.Errorf("load synthetic rimsky.yml: %w", err)
	}
	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		return nil, config.RimskyConfig{}, fmt.Errorf("open persistence for migrate: %w", err)
	}
	if err := driver.Migrate(ctx, shared.NewSlogLogger(logger.With("phase", "migrate"))); err != nil {
		_ = driver.Close()
		return nil, config.RimskyConfig{}, fmt.Errorf("migrate: %w", err)
	}
	return driver, cfg, nil
}

type startUnifiedStackFunc func(context.Context, *slog.Logger, persistence.Database, *config.RimskyConfig, *config.BundledRegistrations) (*launch.UnifiedStack, error)

func StartRoleStack(ctx context.Context, logger *slog.Logger, configPath string, endpoint string) (*RoleStack, error) {
	return startRoleStack(ctx, logger, configPath, endpoint, launch.StartUnifiedStack)
}

func startRoleStack(ctx context.Context, logger *slog.Logger, configPath string, endpoint string, startUnified startUnifiedStackFunc) (*RoleStack, error) {
	driver, cfg, err := MigratePersistence(ctx, logger, configPath)
	if err != nil {
		return nil, err
	}
	bundledRegs, err := bundledwire.CollectBundled(ctx, logger.With("role", "bundled"))
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("register bundled services: %w", err)
	}
	unified, err := startUnified(ctx, logger, driver, &cfg, bundledRegs)
	if err != nil {
		_ = driver.Close()
		return nil, err
	}
	return &RoleStack{
		unified:  unified,
		endpoint: endpoint,
		driver:   driver,
	}, nil
}

func (s *RoleStack) Drain(ctx context.Context, deadline time.Duration) {
	s.drainOnce.Do(func() {
		if s.unified != nil {
			s.unified.Drain(ctx, deadline)
		}
		if s.driver != nil {
			_ = s.driver.Close()
		}
	})
}

func (s *RoleStack) FailCh() <-chan RoleFailure {
	if s.unified == nil {
		return nil
	}
	return s.unified.FailCh()
}

func (s *RoleStack) Endpoint() string { return s.endpoint }

const (
	controlAPIProbeTimeout      = 500 * time.Millisecond
	controlAPIReadyPollInterval = 50 * time.Millisecond
)

func WaitForControlAPIReady(ctx context.Context, clock shared.Clock, endpoint string, deadline time.Duration) error {
	healthURL := endpoint + "/v1/health"
	client := &http.Client{Timeout: controlAPIProbeTimeout}
	var expiry time.Time
	if deadline > 0 {
		expiry = clock.Now().Add(deadline)
	}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return fmt.Errorf("build health request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !expiry.IsZero() && !clock.Now().Before(expiry) {
			return fmt.Errorf("control-api %s not ready within %s", healthURL, deadline)
		}
		if sleepErr := clock.Sleep(ctx, controlAPIReadyPollInterval); sleepErr != nil {
			return sleepErr
		}
	}
}
