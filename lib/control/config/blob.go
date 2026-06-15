// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
)

// sharedMemoryBackendOnce gates lazy construction of sharedMemoryBackend.
var sharedMemoryBackendOnce sync.Once

// sharedMemoryBackend is the process-wide MemoryBackend handed to every
// OpenBlobBackend("memory") caller. The memory backend is gated to the
// single-process mode (RIMSKY_PROCESS_ROLE=unified, set only by the
// all-in-one entrypoint's no-command path), where the three roles run
// in one process but each opens its own persistence driver — a
// per-driver map would silently lose cross-role blob reads and make
// the scheduler's orphan-blob sweep delete from an empty map. One
// shared map per process is the property the gate promises; this
// singleton is what makes it true. Tests constructing MemoryBackend
// directly via persistence.NewMemoryBackend keep isolated instances.
var sharedMemoryBackend *persistence.MemoryBackend

func memoryBackend() *persistence.MemoryBackend {
	sharedMemoryBackendOnce.Do(func() {
		sharedMemoryBackend = persistence.NewMemoryBackend()
	})
	return sharedMemoryBackend
}

// OpenBlobBackend constructs the BlobBackend selected by cfg.Backend
// and installs it on the database via Database.SetBlobBackend. The backend
// is also returned so callers (the supervisor's RunArgs, the orphan
// sweep) can reuse it without round-tripping through the Database.
//
// "inline" returns a no-op InlineBackend; the spill-decision sites
// (`ShouldSpillBlob`) treat it as "spill disabled."
//
// "memory" requires the single-process mode (RIMSKY_PROCESS_ROLE=unified)
// per ValidateBlobConfig, and returns the process-shared MemoryBackend so
// every role's driver in that process reads and sweeps the same map.
//
// "filesystem" requires cfg.Filesystem.Root.
//
// "pg-largeobject" requires the underlying database to be the postgres
// driver — the backend reuses the database's pool. The pgx-isolation
// depguard rule prevents this package from importing pgx directly, so
// the postgres package exposes a NewBlobBackendForDatabase constructor we
// call here.
func OpenBlobBackend(cfg persistence.BlobConfig, db persistence.Database) (persistence.BlobBackend, error) {
	if err := persistence.ValidateBlobConfig(cfg); err != nil {
		return nil, err
	}
	switch cfg.Backend {
	case "", "inline":
		bb := persistence.InlineBackend{}
		db.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "memory":
		bb := memoryBackend()
		db.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "filesystem":
		bb, err := persistence.NewFilesystemBackend(cfg.Filesystem.Root)
		if err != nil {
			return nil, fmt.Errorf("config: open filesystem blob backend: %w", err)
		}
		db.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "pg-largeobject":
		bb, ok := postgres.NewBlobBackendForDatabase(db)
		if !ok {
			return nil, fmt.Errorf("config: pg-largeobject blob backend requires the postgres driver")
		}
		db.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	default:
		return nil, fmt.Errorf("config: unknown blob backend %q", cfg.Backend)
	}
}
