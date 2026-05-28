// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// BlobConfig is parsed from rimsky.yml's persistence.blob block at
// process startup and threaded into the persistence Driver.
//
// The Driver inspects Backend at startup and constructs the matching
// BlobBackend impl. The SpillThresholdBytes field governs when an
// attribute's bytes are written inline (≤ threshold or backend == "inline")
// vs. spilled to the configured backend.
type BlobConfig struct {
	// Backend selects which BlobBackend implementation to use.
	// One of: "inline" | "pg-largeobject" | "filesystem" | "memory".
	// Default: "inline" (no spill).
	Backend string

	// SpillThresholdBytes is the cutoff at which the attribute write path
	// starts spilling to the configured backend instead of writing inline.
	// Default: 65536 (64 KiB).
	SpillThresholdBytes int

	// Filesystem holds the configuration for the "filesystem" backend.
	// Ignored when Backend != "filesystem".
	Filesystem FilesystemBlobConfig

	// PgLargeObject holds the configuration for the "pg-largeobject"
	// backend. Ignored when Backend != "pg-largeobject".
	PgLargeObject PgLargeObjectBlobConfig

	// Retention controls the orphan-blob sweep cadence and grace period.
	Retention BlobRetentionConfig
}

// FilesystemBlobConfig configures the "filesystem" BlobBackend.
type FilesystemBlobConfig struct {
	// Root is the directory under which blob files are written. Default
	// "/var/lib/rimsky/blobs".
	Root string
}

// PgLargeObjectBlobConfig configures the "pg-largeobject" BlobBackend.
type PgLargeObjectBlobConfig struct {
	// Schema is an optional namespace separator. Currently unused; reserved
	// for future deployments that want operational separation.
	Schema string
}

// BlobRetentionConfig governs orphan-blob retention.
type BlobRetentionConfig struct {
	// OrphanSweepInterval is how often SweepOrphanedBlobs runs. Default 1h.
	OrphanSweepInterval time.Duration

	// RetentionAfterUnreferenced is how long an unreferenced blob handle
	// is preserved before deletion. Default 24h. The window allows
	// in-flight readers to complete; very long windows trade disk for
	// safety, very short windows trade safety for prompt cleanup.
	RetentionAfterUnreferenced time.Duration
}

// DefaultBlobConfig returns the conservative default: inline only,
// no spill, with a 64 KiB notional threshold (unused under "inline" but
// present so downstream defaults read consistently).
func DefaultBlobConfig() BlobConfig {
	return BlobConfig{
		Backend:             "inline",
		SpillThresholdBytes: 65536,
		Retention: BlobRetentionConfig{
			OrphanSweepInterval:        time.Hour,
			RetentionAfterUnreferenced: 24 * time.Hour,
		},
	}
}

// ProcessRoleEnv is the env-var name read at startup to decide whether
// the "memory" blob backend's multi-process rejection should fire.
// The unified rimsky-entrypoint sets this to "unified"; the per-process
// binaries either leave it unset or set their own role string.
const ProcessRoleEnv = "RIMSKY_PROCESS_ROLE"

// ErrInvalidBlobConfig is returned by ValidateBlobConfig when the config
// would produce a non-functional or unsafe topology.
var ErrInvalidBlobConfig = errors.New("persistence: invalid blob config")

// ValidateBlobConfig is called by the persistence Driver before
// constructing a BlobBackend. It implements the multi-process safety
// gate for the "memory" backend (per the plan's pre-resolved decision
// and D5): "memory" is dev-only and is rejected unless the process is
// running under the unified entrypoint (RIMSKY_PROCESS_ROLE="unified").
//
// Other validation: the threshold must be non-negative, the filesystem
// root must be non-empty when Backend is "filesystem", and Backend must
// be one of the four supported names.
func ValidateBlobConfig(cfg BlobConfig) error {
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
	if cfg.Backend == "memory" && os.Getenv(ProcessRoleEnv) != "unified" {
		return errInvalidBlobConfigf("memory backend is dev-only and is rejected unless %s=unified (set by rimsky-entrypoint); the per-process binaries (rimsky-scheduler, rimsky-supervisor, rimsky-control-api) cannot share state through an in-process map", ProcessRoleEnv)
	}
	return nil
}

func errInvalidBlobConfigf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidBlobConfig}, args...)...)
}
