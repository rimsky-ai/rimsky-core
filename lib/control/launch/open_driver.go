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

// OpenDriverFromEnv loads the rimsky.yml referenced by RIMSKY_CONFIG
// (or the default path when unset) and opens the persistence driver
// against the parsed persistence block. Caller owns the lifecycle:
// defer driver.Close(). Returns the open driver plus the parsed
// config; the config is handed back so the caller can pass it on to
// the role runners that need cfg.Stores / cfg.NamedLocks /
// cfg.Executors / etc. without re-reading the file.
//
// This is the single Open site every Run* role-runner caller funnels
// through. The runners themselves never open a driver — they take
// the open driver as a parameter — which is what keeps unified mode
// honest about writer-slot contention.
//
// @blessed-invariant: one-driver-per-process — every Run* runner in
// one process shares the SAME persistence.Database instance, so the
// sqlite per-driver writer slot is not contended across roles in
// unified mode. A caller that opens additional drivers against the
// same file re-introduces the contention this helper exists to
// prevent.
func OpenDriverFromEnv(ctx context.Context, logger *slog.Logger) (persistence.Database, *config.RimskyConfig, error) {
	configPath := os.Getenv("RIMSKY_CONFIG")
	if configPath == "" {
		configPath = defaultRimskyConfigPath
	}
	cfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		logger.Error("load rimsky config", "error", err.Error(), "path", configPath)
		return nil, nil, fmt.Errorf("load rimsky config %q: %w", configPath, err)
	}
	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		logger.Error("persistence.Open", "error", err.Error())
		return nil, nil, fmt.Errorf("persistence.Open: %w", err)
	}
	return driver, &cfg, nil
}
