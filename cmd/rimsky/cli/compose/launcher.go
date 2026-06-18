// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

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

var startRoleStackFn = launch.StartUnifiedStack

func StartRoleStack(ctx context.Context, logger *slog.Logger, configPath string, endpoint string) (*RoleStack, error) {
	driver, cfg, err := MigratePersistence(ctx, logger, configPath)
	if err != nil {
		return nil, err
	}
	unified, err := startRoleStackFn(ctx, logger, driver, &cfg)
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

func WaitForControlAPIReady(ctx context.Context, endpoint string, deadline time.Duration) error {
	healthURL := endpoint + "/v1/health"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, healthURL, nil)
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
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("control-api %s not ready within %s", healthURL, deadline)
		case <-ticker.C:
		}
	}
}
