// Package config provides library entry points for rimsky's three long-running
// processes: scheduler, supervisor, control API. Each entry point takes a
// typed config struct and returns a handle with a Shutdown method.
package config

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// SchedulerConfig wires a scheduler process. Defaults apply when fields
// are zero values.
type SchedulerConfig struct {
	Storage              storage.StorageBackend
	Queue                queue.DispatchQueue
	Clock                shared.Clock
	Logger               shared.Logger
	TickInterval         time.Duration // default 1500ms
	HeartbeatTimeout     time.Duration // default 15s
	OrphanedClaimTimeout time.Duration // default 5×HeartbeatTimeout
	Pool                 *pgxpool.Pool // for advisory lock; optional but recommended
}

// SchedulerHandle exposes graceful shutdown for a running scheduler process.
type SchedulerHandle interface {
	Shutdown(ctx context.Context) error
}

// StartScheduler starts a scheduler process and returns a handle for
// graceful shutdown.
func StartScheduler(cfg SchedulerConfig) (SchedulerHandle, error) {
	// Default-fill via scheduler.Config.
	inner := scheduler.Config{
		Storage:              cfg.Storage,
		Queue:                cfg.Queue,
		Clock:                cfg.Clock,
		Logger:               cfg.Logger,
		TickInterval:         cfg.TickInterval,
		HeartbeatTimeout:     cfg.HeartbeatTimeout,
		OrphanedClaimTimeout: cfg.OrphanedClaimTimeout,
		Pool:                 cfg.Pool,
	}
	return scheduler.Start(inner), nil
}
