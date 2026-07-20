// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package launch

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const defaultRimskyConfigPath = "/etc/rimsky/rimsky.yml"

func resolveConfigPath(envValue string) string {
	if envValue == "" {
		return defaultRimskyConfigPath
	}
	return envValue
}

func OpenDriverFromEnv(ctx context.Context, logger *slog.Logger) (persistence.Database, *config.RimskyConfig, error) {
	configPath := resolveConfigPath(os.Getenv("RIMSKY_CONFIG"))
	cfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		logger.Error("load rimsky config", "error", err.Error(), "path", configPath)
		return nil, nil, fmt.Errorf("load rimsky config %q: %w", configPath, err)
	}
	for _, w := range cfg.Warnings {
		logger.Warn(w)
	}
	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		logger.Error("persistence.Open", "error", err.Error())
		return nil, nil, fmt.Errorf("persistence.Open: %w", err)
	}
	return driver, &cfg, nil
}
