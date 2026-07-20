// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"errors"
	"fmt"
	"time"
)

type BlobConfig struct {
	Backend string

	SpillThresholdBytes int

	Filesystem FilesystemBlobConfig

	PgLargeObject PgLargeObjectBlobConfig

	Retention BlobRetentionConfig
}

type FilesystemBlobConfig struct {
	Root string
}

type PgLargeObjectBlobConfig struct {
	Schema string
}

type BlobRetentionConfig struct {
	OrphanSweepInterval time.Duration

	RetentionAfterUnreferenced time.Duration
}

func DefaultBlobConfig() BlobConfig {
	return BlobConfig{
		Backend:             "inline",
		SpillThresholdBytes: 65536,
		Retention: BlobRetentionConfig{
			OrphanSweepInterval:        time.Hour,
			RetentionAfterUnreferenced: DefaultOrphanRetention,
		},
	}
}

const ProcessRoleEnv = "RIMSKY_PROCESS_ROLE"

var ErrInvalidBlobConfig = errors.New("persistence: invalid blob config")

func ValidateBlobConfig(cfg BlobConfig, topology Topology) error {
	switch cfg.Backend {
	case "", "inline", "pg-largeobject", "filesystem", "memory":
	default:
		return errInvalidBlobConfigf("unknown backend %q (want one of inline | pg-largeobject | filesystem | memory)", cfg.Backend)
	}
	if cfg.SpillThresholdBytes < 0 {
		return errInvalidBlobConfigf("spill_threshold_bytes must be >= 0, got %d", cfg.SpillThresholdBytes)
	}
	if cfg.Backend == "filesystem" && cfg.Filesystem.Root == "" {
		return errInvalidBlobConfigf("filesystem backend requires filesystem.root")
	}
	// @decision: process-role-unified-message-covers-rimsky-run
	// @concept: blob-backend
	if cfg.Backend == "memory" && !topology.Unified() {
		return errInvalidBlobConfigf("memory backend is dev-only and requires the single-process mode: all roles in one process sharing one in-process blob map, marked by %s=%s (set only by rimsky-entrypoint's no-command all-in-one path, by rimsky compose run, and by rimsky run in self-host mode); a per-role process cannot share an in-process map with the other roles", ProcessRoleEnv, TopologyUnified)
	}
	return nil
}

func errInvalidBlobConfigf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidBlobConfig}, args...)...)
}
