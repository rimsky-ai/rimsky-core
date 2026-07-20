// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/cmd/internal/roleboot"
	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func main() {
	roleboot.Main(func(ctx context.Context, logger *slog.Logger, driver persistence.Database, cfg *config.RimskyConfig) (launch.StopFunc, <-chan error, error) {
		return launch.RunScheduler(ctx, logger, driver, cfg, nil)
	})
}
