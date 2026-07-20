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

var sharedMemoryBackendOnce sync.Once

var sharedMemoryBackend *persistence.MemoryBackend

func memoryBackend() *persistence.MemoryBackend {
	sharedMemoryBackendOnce.Do(func() {
		sharedMemoryBackend = persistence.NewMemoryBackend()
	})
	return sharedMemoryBackend
}

// @concept: blob-backend
func OpenBlobBackend(cfg persistence.BlobConfig, db persistence.Database, topology persistence.Topology) (persistence.BlobBackend, error) {
	if err := persistence.ValidateBlobConfig(cfg, topology); err != nil {
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

// @concept: blob-backend
func WireBlobBackend(cfg persistence.BlobConfig, db persistence.Database, topology persistence.Topology) error {
	_, err := OpenBlobBackend(cfg, db, topology)
	return err
}
