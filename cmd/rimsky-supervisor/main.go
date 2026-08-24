// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
		return launch.RunSupervisor(ctx, logger, driver, cfg, launch.RoleOptions{})
	})
}
