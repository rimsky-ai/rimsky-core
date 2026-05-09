// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/persistence/postgres"
)

// OpenBlobBackend constructs the BlobBackend selected by cfg.Backend
// and installs it on the driver via Driver.SetBlobBackend. The backend
// is also returned so callers (the supervisor's RunArgs, the orphan
// sweep) can reuse it without round-tripping through the Driver.
//
// "inline" returns a no-op InlineBackend; the spill-decision sites
// (`ShouldSpillBlob`) treat it as "spill disabled."
//
// "memory" requires RIMSKY_PROCESS_ROLE=unified per ValidateBlobConfig.
//
// "filesystem" requires cfg.Filesystem.Root.
//
// "pg-largeobject" requires the underlying driver to be the postgres
// driver — the backend reuses the driver's pool. The pgx-isolation
// depguard rule prevents this package from importing pgx directly, so
// the postgres driver exposes a NewBlobBackendForDriver constructor we
// call here.
func OpenBlobBackend(ctx context.Context, cfg persistence.BlobConfig, drv persistence.Driver) (persistence.BlobBackend, error) {
	if err := persistence.ValidateBlobConfig(cfg); err != nil {
		return nil, err
	}
	switch cfg.Backend {
	case "", "inline":
		bb := persistence.InlineBackend{}
		drv.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "memory":
		bb := persistence.NewMemoryBackend()
		drv.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "filesystem":
		bb, err := persistence.NewFilesystemBackend(cfg.Filesystem.Root)
		if err != nil {
			return nil, fmt.Errorf("config: open filesystem blob backend: %w", err)
		}
		drv.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	case "pg-largeobject":
		bb, ok := postgres.NewBlobBackendForDriver(drv)
		if !ok {
			return nil, fmt.Errorf("config: pg-largeobject blob backend requires the postgres driver")
		}
		drv.SetBlobBackend(bb, cfg.SpillThresholdBytes, cfg.Retention.RetentionAfterUnreferenced)
		return bb, nil
	default:
		return nil, fmt.Errorf("config: unknown blob backend %q", cfg.Backend)
	}
}
